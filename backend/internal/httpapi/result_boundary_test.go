package httpapi_test

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/s3eetdev/AI-buddy/backend/internal/evidence"
	"github.com/s3eetdev/AI-buddy/backend/internal/storage/postgres"
)

func TestStoredResultRejectsStaleVersionAndChangedCorpus(t *testing.T) {
	store := testStore(t)
	server, corpus := setup(t, store)
	api := httptest.NewServer(server.Handler())
	defer api.Close()
	code, m := call(t, api.URL, "POST", "/api/v1/analyses", "result-boundary", `{"jira_url":"https://demo-jira.local/browse/DEMO-1"}`)
	expect(t, code, 202, m)
	id := m["analysis_id"].(string)
	j, err := store.ClaimJob(context.Background(), time.Second, 2)
	if err != nil || j == nil {
		t.Fatalf("claim: %v", err)
	}
	raw, err := os.ReadFile("../../../examples/recorded/DEMO-1.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.FinishJob(context.Background(), *j, postgres.Completion{Status: "ready", Result: raw, AIRequest: j.Input, CorpusDigest: corpus.Digest(), ExecutionMode: "live"}); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/analyses/" + id
	for _, tc := range []struct {
		method, path, key, body string
		status                  int
	}{
		{"GET", base + "?result_revision=0", "", "", 400},
		{"GET", base + "/sources/missing?result_revision=0", "", "", 400},
		{"GET", base + "/sources/missing?result_revision=1", "", "", 404},
		{"PATCH", base + "/steps/v2-s1", "", `{"result_revision":2,"completed":true}`, 409},
		{"PATCH", base + "/steps/v1-s999", "", `{"result_revision":1,"completed":true}`, 404},
		{"PATCH", base + "/steps/v1-s1", "", `{"extra":true}`, 400},
		{"POST", base + "/answers", "empty-answer", `{"answer":"  "}`, 422},
		{"POST", base + "/answers", "bad-answer", `{"unknown":true}`, 400},
		{"POST", base + "/publish", "", `{"result_revision":1,"confirmed":true}`, 400},
		{"POST", base + "/publish", "bad-publish", `{"unknown":true}`, 400},
		{"POST", base + "/publish", "stale-publish", `{"result_revision":2,"confirmed":true}`, 409},
		{"GET", "/api/v1/mock-jira/plans/missing", "", "", 404},
	} {
		code, m = call(t, api.URL, tc.method, tc.path, tc.key, tc.body)
		expect(t, code, tc.status, m)
	}
	content, err := os.ReadFile("../../../data/demo_sources.json")
	if err != nil {
		t.Fatal(err)
	}
	changedPath := filepath.Join(t.TempDir(), "corpus.json")
	if err = os.WriteFile(changedPath, append(content, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := evidence.Load(changedPath)
	if err != nil {
		t.Fatal(err)
	}
	server.Corpus = changed
	for _, path := range []string{base, "/api/v1/analyses"} {
		code, m = call(t, api.URL, "GET", path, "", "")
		expect(t, code, 409, m)
		if m["error"].(map[string]any)["code"] != "corpus_mismatch" {
			t.Fatal(m)
		}
	}
	code, m = call(t, api.URL, "POST", "/api/v1/analyses", "result-boundary", `{"jira_url":"https://demo-jira.local/browse/DEMO-1"}`)
	expect(t, code, 409, m)
	server.Corpus = corpus
	if _, err = store.Pool.Exec(context.Background(), `UPDATE analysis_results SET result='{}'::jsonb WHERE analysis_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	code, m = call(t, api.URL, "GET", base, "", "")
	expect(t, code, 500, m)
	if m["error"].(map[string]any)["code"] != "invalid_stored_result" {
		t.Fatal(m)
	}
	store.Close()
	code, m = call(t, api.URL, "GET", "/api/v1/analyses", "", "")
	expect(t, code, 500, m)
}
