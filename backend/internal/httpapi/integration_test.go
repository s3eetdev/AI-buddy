package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/s3eetdev/AI-buddy/backend/internal/config"
	"github.com/s3eetdev/AI-buddy/backend/internal/evidence"
	"github.com/s3eetdev/AI-buddy/backend/internal/httpapi"
	"github.com/s3eetdev/AI-buddy/backend/internal/integrations/ai"
	"github.com/s3eetdev/AI-buddy/backend/internal/integrations/jira"
	"github.com/s3eetdev/AI-buddy/backend/internal/jobs"
	"github.com/s3eetdev/AI-buddy/backend/internal/storage/postgres"
)

// Every run uses a new schema, and never clears existing user data.
func testStore(t *testing.T) *postgres.Store {
	t.Helper()
	addr := os.Getenv("TEST_DATABASE_URL")
	if addr == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, addr)
	if e != nil {
		t.Fatal(e)
	}
	if e = admin.Ping(ctx); e != nil {
		t.Fatal(e)
	}
	schema := "test_" + bytesToHex(httpapi.ID())
	ident := pgx.Identifier{schema}.Sanitize()
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+ident); e != nil {
		t.Fatal(e)
	}
	cfg, e := pgxpool.ParseConfig(addr)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	store := &postgres.Store{Pool: pool}
	if e = store.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+ident+" CASCADE")
		admin.Close()
	})
	return store
}
func bytesToHex(s string) string {
	var b bytes.Buffer
	for _, c := range s {
		if c != '-' {
			b.WriteRune(c)
		}
	}
	return b.String()
}
func call(t *testing.T, base, method, path, key, body string) (int, map[string]any) {
	t.Helper()
	req, e := http.NewRequest(method, base+path, bytes.NewBufferString(body))
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	client := http.Client{Timeout: 3 * time.Second}
	res, e := client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	var m map[string]any
	if e = json.Unmarshal(data, &m); e != nil {
		t.Fatalf("%s %s: %s", method, path, data)
	}
	return res.StatusCode, m
}
func expect(t *testing.T, got, want int, m map[string]any) {
	t.Helper()
	if got != want {
		t.Fatalf("HTTP %d expected %d: %v", got, want, m)
	}
}
func waitStatus(t *testing.T, base, id, status string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		code, m := call(t, base, "GET", "/api/v1/analyses/"+id, "", "")
		expect(t, code, 200, m)
		if m["status"] == status {
			return m
		}
		if m["status"] == "failed" {
			t.Fatalf("worker failed: %v", m)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", status)
	return nil
}
func setup(t *testing.T, store *postgres.Store) (*httpapi.Server, *evidence.Corpus) {
	t.Helper()
	seed, e := jira.Load("../../../data/demo_tasks.json")
	if e != nil {
		t.Fatal(e)
	}
	corpus, e := evidence.Load("../../../data/demo_sources.json")
	if e != nil {
		t.Fatal(e)
	}
	return &httpapi.Server{Config: config.Config{Profile: "intern", AIMode: "live", JiraBaseURL: "https://demo-jira.local"}, Store: store, Seed: seed, Corpus: corpus}, corpus
}

func TestPostgresHTTPWorkflow(t *testing.T) {
	store := testStore(t)
	server, corpus := setup(t, store)
	raw, e := os.ReadFile("../../../examples/recorded/DEMO-2.json")
	if e != nil {
		t.Fatal(e)
	}
	var recorded ai.Response
	if e = json.Unmarshal(raw, &recorded); e != nil {
		t.Fatal(e)
	}
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var mu sync.Mutex
	calls := []ai.Request{}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/analyze" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("incorrect AI contract")
		}
		var req ai.Request
		if e := json.NewDecoder(r.Body).Decode(&req); e != nil {
			t.Error(e)
			return
		}
		mu.Lock()
		calls = append(calls, req)
		n := len(calls)
		mu.Unlock()
		if n == 1 {
			entered <- struct{}{}
			<-release
		}
		response := recorded
		if n > 1 {
			b, _ := json.Marshal(recorded)
			_ = json.Unmarshal(b, &response)
			response.Status = "ready"
			response.Plan.Questions = []string{}
			for i := range response.Sources {
				if response.Sources[i].ID == "task:"+req.TaskID {
					response.Sources[i].Content = ai.TaskSourceContent(req)
				}
			}
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer fake.Close()
	client := ai.NewLive(fake.URL, "test-token", 3*time.Second)
	process := func(ctx context.Context, j postgres.Job) (postgres.Completion, error) {
		var req ai.Request
		if e := json.Unmarshal(j.Input, &req); e != nil {
			return postgres.Completion{}, e
		}
		res, e := client.Analyze(ctx, req)
		if e != nil {
			return postgres.Completion{}, e
		}
		if e = corpus.Validate(req, res); e != nil {
			return postgres.Completion{}, e
		}
		b, e := json.Marshal(res)
		return postgres.Completion{Result: b, AIRequest: j.Input, CorpusDigest: corpus.Digest(), Status: res.Status, ExecutionMode: j.ExecutionMode}, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		jobs.Run(ctx, store, process, jobs.Config{Concurrency: 2, Lease: 2 * time.Second, Heartbeat: 200 * time.Millisecond, MaxAttempts: 2})
		close(done)
	}()
	defer func() { cancel(); <-done }()
	api := httptest.NewServer(server.Handler())
	defer api.Close()
	create := `{"jira_url":"https://demo-jira.local/browse/DEMO-2"}`
	started := time.Now()
	code, m := call(t, api.URL, "POST", "/api/v1/analyses", "create-1", create)
	expect(t, code, 202, m)
	if time.Since(started) > 2*time.Second {
		t.Fatal("create waited for AI")
	}
	id := m["analysis_id"].(string)
	select {
	case <-entered:
	case <-time.After(4 * time.Second):
		t.Fatal("fake AI was not called")
	}
	code, m = call(t, api.URL, "POST", "/api/v1/analyses", "create-1", create)
	expect(t, code, 202, m)
	if m["analysis_id"] != id {
		t.Fatal("duplicate create")
	}
	code, m = call(t, api.URL, "POST", "/api/v1/analyses", "create-1", `{"jira_url":"https://demo-jira.local/browse/DEMO-1"}`)
	expect(t, code, 409, m)
	code, m = call(t, api.URL, "POST", "/api/v1/analyses", "injected", `{"jira_url":"https://demo-jira.local/browse/DEMO-2","allowed_scopes":["legacy"]}`)
	expect(t, code, 400, m)
	releaseOnce.Do(func() { close(release) })
	waitStatus(t, api.URL, id, "needs_clarification")
	code, m = call(t, api.URL, "POST", "/api/v1/analyses/"+id+"/publish", "p0", `{"result_revision":1,"confirmed":true}`)
	expect(t, code, 409, m)
	code, m = call(t, api.URL, "POST", "/api/v1/analyses/"+id+"/answers", "answer-1", `{"answer":"Канал email"}`)
	expect(t, code, 202, m)
	code, m = call(t, api.URL, "POST", "/api/v1/analyses/"+id+"/answers", "answer-1", `{"answer":"Канал email"}`)
	expect(t, code, 202, m)
	m = waitStatus(t, api.URL, id, "ready")
	if m["result_revision"] != float64(2) {
		t.Fatalf("incorrect revision: %v", m)
	}
	// A network retry can arrive after the second version is already ready.
	code, m = call(t, api.URL, "POST", "/api/v1/analyses/"+id+"/answers", "answer-1", `{"answer":"Канал email"}`)
	expect(t, code, 202, m)
	code, m = call(t, api.URL, "POST", "/api/v1/analyses/"+id+"/answers", "answer-1", `{"answer":"Другой ответ"}`)
	expect(t, code, 409, m)
	mu.Lock()
	if len(calls) != 2 || calls[1].Question != "Канал email" || calls[0].Title != calls[1].Title {
		t.Errorf("unexpected AI attempts: %+v", calls)
	}
	mu.Unlock()
	answers, e := store.Answers(context.Background(), "intern", id)
	if e != nil || len(answers) != 1 || answers[0] != "Канал email" {
		t.Fatalf("answers: %v %v", answers, e)
	}
	code, m = call(t, api.URL, "GET", "/api/v1/analyses/"+id+"?result_revision=1", "", "")
	expect(t, code, 200, m)
	if m["result_revision"] != float64(1) {
		t.Fatal("old revision unavailable")
	}
	code, m = call(t, api.URL, "GET", "/api/v1/analyses/"+id+"/sources/cancel-decision?result_revision=2", "", "")
	expect(t, code, 200, m)
	if m["content"] == "" {
		t.Fatal("empty source")
	}
	code, m = call(t, api.URL, "GET", "/api/v1/analyses/"+id, "", "")
	expect(t, code, 200, m)
	if len(m["step_progress"].([]any)) != 0 {
		t.Fatal("reading a source changed progress")
	}
	code, m = call(t, api.URL, "PATCH", "/api/v1/analyses/"+id+"/steps/v2-s1", "", `{"result_revision":2,"completed":true}`)
	expect(t, code, 200, m)
	if m["completed"] != true {
		t.Fatal("progress missing")
	}
	code, m = call(t, api.URL, "POST", "/api/v1/analyses/"+id+"/publish", "p1", `{"result_revision":2}`)
	expect(t, code, 422, m)
	type result struct {
		code int
		m    map[string]any
	}
	out := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func(n int) {
			code, m := call(t, api.URL, "POST", "/api/v1/analyses/"+id+"/publish", fmt.Sprintf("p%d", n+1), `{"result_revision":2,"confirmed":true}`)
			out <- result{code, m}
		}(i)
	}
	a, b := <-out, <-out
	expect(t, a.code, 200, a.m)
	expect(t, b.code, 200, b.m)
	if a.m["plan_id"] != b.m["plan_id"] {
		t.Fatal("duplicate plans")
	}
	url := a.m["url"].(string)
	code, m = call(t, api.URL, "GET", url, "", "")
	expect(t, code, 200, m)
	if m["jira_markdown"] != recorded.JiraMarkdown {
		t.Fatal("publication changed markdown")
	}
	code, m = call(t, api.URL, "POST", "/api/v1/analyses/"+id+"/publish", "p1", `{"result_revision":1,"confirmed":true}`)
	expect(t, code, 409, m)
	other := *server
	other.Config.Profile = "reviewer"
	outsider := httptest.NewServer(other.Handler())
	defer outsider.Close()
	for _, path := range []string{"/api/v1/analyses/" + id, "/api/v1/analyses/" + id + "/sources/cancel-decision", url} {
		code, m = call(t, outsider.URL, "GET", path, "", "")
		expect(t, code, 404, m)
	}
	// The owner remains the same, but removing the scope must revoke old snapshots.
	revoked := *server
	seedCopy := *server.Seed
	seedCopy.Profiles = append([]jira.Profile(nil), server.Seed.Profiles...)
	for i := range seedCopy.Profiles {
		if seedCopy.Profiles[i].ID == "intern" {
			seedCopy.Profiles[i].AllowedScopes = []string{}
		}
	}
	revoked.Seed = &seedCopy
	revokedAPI := httptest.NewServer(revoked.Handler())
	defer revokedAPI.Close()
	for _, path := range []string{"/api/v1/analyses/" + id, "/api/v1/analyses/" + id + "/sources/cancel-decision", url} {
		code, m = call(t, revokedAPI.URL, "GET", path, "", "")
		expect(t, code, 403, m)
	}
}

func TestPostgresLeaseFencing(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	in := postgres.CreateInput{ID: httpapi.ID(), JobID: httpapi.ID(), Owner: "intern", JiraURL: "https://demo-jira.local/browse/DEMO-1", TaskID: "DEMO-1", IdempotencyKey: "fence", PayloadHash: "a", ExecutionMode: "live", TaskSnapshot: json.RawMessage(`{}`), AIRequest: json.RawMessage(`{}`)}
	if _, e := store.CreateAnalysis(ctx, in); e != nil {
		t.Fatal(e)
	}
	first, e := store.ClaimJob(ctx, time.Minute, 2)
	if e != nil || first == nil {
		t.Fatalf("claim %v", e)
	}
	second, e := store.ClaimJob(ctx, time.Minute, 2)
	if e != nil || second != nil {
		t.Fatalf("concurrent claim: %+v %v", second, e)
	}
	if _, e = store.Pool.Exec(ctx, `UPDATE analysis_jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, first.ID); e != nil {
		t.Fatal(e)
	}
	second, e = store.ClaimJob(ctx, time.Minute, 2)
	if e != nil || second == nil || second.Attempt != 2 {
		t.Fatalf("reclaim %+v %v", second, e)
	}
	c := postgres.Completion{Result: json.RawMessage(`{}`), AIRequest: json.RawMessage(`{}`), Status: "ready", ExecutionMode: "live", CorpusDigest: "x"}
	if e = store.FinishJob(ctx, *first, c); !errors.Is(e, postgres.ErrLeaseLost) {
		t.Fatalf("stale claim accepted: %v", e)
	}
	if e = store.FinishJob(ctx, *second, c); e != nil {
		t.Fatal(e)
	}
}
