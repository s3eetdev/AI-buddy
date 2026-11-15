package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/s3eetdev/AI-buddy/backend/internal/integrations/ai"
	"github.com/s3eetdev/AI-buddy/backend/internal/storage/postgres"
)

func TestPostgresHTTPPublicationFailureAndRetry(t *testing.T) {
	store := testStore(t)
	server, corpus := setup(t, store)
	api := httptest.NewServer(server.Handler())
	defer api.Close()
	code, created := call(t, api.URL, "POST", "/api/v1/analyses", "create-fail", `{"jira_url":"https://demo-jira.local/browse/DEMO-1"}`)
	expect(t, code, 202, created)
	id := created["analysis_id"].(string)
	ctx := context.Background()
	job, err := store.ClaimJob(ctx, 5*time.Second, 2)
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	var req ai.Request
	if err = json.Unmarshal(job.Input, &req); err != nil {
		t.Fatal(err)
	}
	replay, err := ai.NewReplay("../../../examples/recorded", "../../../data/demo_tasks.json")
	if err != nil {
		t.Fatal(err)
	}
	response, err := replay.Analyze(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if err = corpus.Validate(req, response); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.FinishJob(ctx, *job, postgres.Completion{Result: raw, AIRequest: job.Input, CorpusDigest: corpus.Digest(), Status: response.Status, ExecutionMode: "replay"}); err != nil {
		t.Fatal(err)
	}
	_, err = store.Pool.Exec(ctx, `CREATE FUNCTION reject_mock_plan() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'SECRET-INTERNAL-DATABASE-ERROR'; END $$`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Pool.Exec(ctx, `CREATE TRIGGER reject_mock_plan BEFORE INSERT ON mock_jira_plans FOR EACH ROW EXECUTE FUNCTION reject_mock_plan()`)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/analyses/" + id + "/publish"
	body := `{"result_revision":1,"confirmed":true}`
	code, failed := call(t, api.URL, "POST", path, "publish-fail", body)
	expect(t, code, 500, failed)
	b, _ := json.Marshal(failed)
	if strings.Contains(string(b), "SECRET-INTERNAL") {
		t.Fatalf("private database error leaked: %s", b)
	}
	code, result := call(t, api.URL, "GET", "/api/v1/analyses/"+id, "", "")
	expect(t, code, 200, result)
	pub, ok := result["publication"].(map[string]any)
	if !ok || pub["status"] != "publish_failed" {
		t.Fatalf("publication status: %v", result)
	}
	b, _ = json.Marshal(pub)
	if !strings.Contains(string(b), "publish_failed") || strings.Contains(string(b), "SECRET-INTERNAL") {
		t.Fatalf("publication error: %s", b)
	}
	if _, err = store.Pool.Exec(ctx, `DROP TRIGGER reject_mock_plan ON mock_jira_plans`); err != nil {
		t.Fatal(err)
	}
	code, published := call(t, api.URL, "POST", path, "publish-fail", body)
	expect(t, code, 200, published)
	plan := published["plan_id"].(string)
	code, repeated := call(t, api.URL, "POST", path, "another-key", body)
	expect(t, code, 200, repeated)
	if repeated["plan_id"] != plan {
		t.Fatalf("duplicate plan: %v %v", published, repeated)
	}
	code, read := call(t, api.URL, "GET", published["url"].(string), "", "")
	expect(t, code, 200, read)
	if read["plan_id"] != plan {
		t.Fatalf("wrong plan: %v", read)
	}
}
