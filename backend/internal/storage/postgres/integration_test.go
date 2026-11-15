package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func integrationStore(t *testing.T) (*Store, *pgxpool.Config) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	base, e := pgxpool.New(ctx, url)
	if e != nil {
		t.Fatal(e)
	}
	var b [8]byte
	if _, e = rand.Read(b[:]); e != nil {
		t.Fatal(e)
	}
	schema := "backend_test_" + hex.EncodeToString(b[:])
	if _, e = base.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		base.Close()
		t.Fatal(e)
	}
	cfg, e := pgxpool.ParseConfig(url)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	p, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	s := &Store{Pool: p}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		p.Close()
		_, e := base.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		if e != nil {
			t.Error(e)
		}
		base.Close()
	})
	return s, cfg
}
func makeAnalysis(t *testing.T, s *Store, key string) *Analysis {
	t.Helper()
	a, e := s.CreateAnalysis(context.Background(), CreateInput{Owner: "intern", JiraURL: "https://demo-jira.local/browse/DEMO-1", TaskID: "DEMO-1", IdempotencyKey: key, PayloadHash: key, ExecutionMode: "replay", TaskSnapshot: json.RawMessage(`{"task_id":"DEMO-1"}`), AIRequest: json.RawMessage(`{"task_id":"DEMO-1"}`)})
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func resultCompletion() Completion {
	return Completion{Status: "ready", ExecutionMode: "replay", CorpusDigest: "digest", AIRequest: json.RawMessage(`{"task_id":"DEMO-1"}`), Result: json.RawMessage(`{"task_id":"DEMO-1","status":"ready","plan":{"findings":[{}],"steps":[{}],"questions":[]},"sources":[{}],"jira_markdown":"# plan"}`)}
}
func TestIntegrationLeaseFencingAndPublish(t *testing.T) {
	s, cfg := integrationStore(t)
	ctx := context.Background()
	a := makeAnalysis(t, s, "create-a")
	again, e := s.CreateAnalysis(ctx, CreateInput{Owner: "intern", IdempotencyKey: "create-a", PayloadHash: "create-a"})
	if e != nil || again.ID != a.ID {
		t.Fatalf("idempotent create: %v %+v", e, again)
	}
	_, e = s.CreateAnalysis(ctx, CreateInput{Owner: "intern", IdempotencyKey: "create-a", PayloadHash: "different"})
	if !errors.Is(e, ErrConflict) {
		t.Fatalf("create conflict: %v", e)
	}
	var wg sync.WaitGroup
	claims := make(chan *Job, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); j, e := s.ClaimJob(ctx, 150*time.Millisecond, 2); claims <- j; errs <- e }()
	}
	wg.Wait()
	close(claims)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var first *Job
	count := 0
	for j := range claims {
		if j != nil {
			first = j
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected one claim, got %d", count)
	}
	time.Sleep(200 * time.Millisecond)
	second, e := s.ClaimJob(ctx, 5*time.Second, 2)
	if e != nil || second == nil {
		t.Fatalf("reclaim: %v", e)
	}
	if first.ClaimToken == second.ClaimToken || second.Attempt != 2 {
		t.Fatal("reclaim did not fence old worker")
	}
	if e = s.Heartbeat(ctx, *first, time.Second); !errors.Is(e, ErrLeaseLost) {
		t.Fatalf("stale heartbeat: %v", e)
	}
	if e = s.FinishJob(ctx, *first, resultCompletion()); !errors.Is(e, ErrLeaseLost) {
		t.Fatalf("stale completion: %v", e)
	}
	if e = s.FinishJob(ctx, *second, resultCompletion()); e != nil {
		t.Fatal(e)
	}
	plans := make(chan string, 8)
	errs2 := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p, e := s.Publish(ctx, PublishInput{Owner: "intern", AnalysisID: a.ID, Revision: 1, IdempotencyKey: fmt.Sprintf("publish-%d", i), PayloadHash: "same"})
			if e == nil {
				plans <- p.ID
			}
			errs2 <- e
		}(i)
	}
	wg.Wait()
	close(plans)
	close(errs2)
	for e := range errs2 {
		if e != nil {
			t.Fatal(e)
		}
	}
	planID := ""
	for id := range plans {
		if planID != "" && id != planID {
			t.Fatal("duplicate plans")
		}
		planID = id
	}
	if planID == "" {
		t.Fatal("no plan")
	}
	_, e = s.Publish(ctx, PublishInput{Owner: "intern", AnalysisID: a.ID, Revision: 1, IdempotencyKey: "publish-0", PayloadHash: "different"})
	if !errors.Is(e, ErrConflict) {
		t.Fatalf("publish conflict: %v", e)
	}
	reopened, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	other := &Store{Pool: reopened}
	p, e := other.GetPlan(ctx, "intern", planID)
	if e != nil || p.Markdown != "# plan" {
		t.Fatalf("persistent plan: %+v %v", p, e)
	}
	if _, e = other.GetPlan(ctx, "reviewer", planID); !errors.Is(e, ErrNotFound) {
		t.Fatalf("foreign plan: %v", e)
	}
	got, e := other.GetAnalysis(ctx, "intern", a.ID, nil)
	if e != nil || got.Status != "ready" || got.ResultRevision == nil || *got.ResultRevision != 1 {
		t.Fatalf("persistent result: %+v %v", got, e)
	}
}
func TestIntegrationAttemptLimit(t *testing.T) {
	s, _ := integrationStore(t)
	ctx := context.Background()
	a := makeAnalysis(t, s, "create-limit")
	j, e := s.ClaimJob(ctx, 100*time.Millisecond, 1)
	if e != nil || j == nil {
		t.Fatalf("claim: %v", e)
	}
	time.Sleep(150 * time.Millisecond)
	j, e = s.ClaimJob(ctx, time.Second, 1)
	if e != nil || j != nil {
		t.Fatalf("exhausted job reclaimed: %+v %v", j, e)
	}
	a, e = s.GetAnalysis(ctx, "intern", a.ID, nil)
	if e != nil || a.Status != "failed" || a.Error == nil || a.Error.Code != "job_attempts_exhausted" {
		t.Fatalf("exhausted state: %+v %v", a, e)
	}
}

func TestIntegrationAnswersAndVersionedProgress(t *testing.T) {
	s, _ := integrationStore(t)
	ctx := context.Background()
	a := makeAnalysis(t, s, "create-answers")
	job, e := s.ClaimJob(ctx, time.Second*5, 2)
	if e != nil || job == nil {
		t.Fatalf("claim: %v", e)
	}
	first := resultCompletion()
	first.Status = "needs_clarification"
	first.Result = json.RawMessage(`{"task_id":"DEMO-1","status":"needs_clarification","plan":{"findings":[{}],"steps":[{}],"questions":["question"]},"sources":[{}],"jira_markdown":"draft"}`)
	if e = s.FinishJob(ctx, *job, first); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetProgress(ctx, "intern", a.ID, 1, "v1-s1", true); e != nil {
		t.Fatal(e)
	}
	type outcome struct {
		key string
		err error
	}
	ch := make(chan outcome, 2)
	var wg sync.WaitGroup
	for _, key := range []string{"answer-a", "answer-b"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			_, err := s.SubmitAnswer(ctx, AnswerInput{AnalysisID: a.ID, Owner: "intern", IdempotencyKey: key, PayloadHash: key, Answer: key, ExpectedRevision: 1, AIRequest: json.RawMessage(`{"task_id":"DEMO-1","question":"answer"}`)})
			ch <- outcome{key, err}
		}(key)
	}
	wg.Wait()
	close(ch)
	winner := ""
	for v := range ch {
		if v.err == nil {
			if winner != "" {
				t.Fatal("two active jobs created")
			}
			winner = v.key
		} else if !errors.Is(v.err, ErrConflict) {
			t.Fatal(v.err)
		}
	}
	if winner == "" {
		t.Fatal("no answer accepted")
	}
	_, e = s.SubmitAnswer(ctx, AnswerInput{AnalysisID: a.ID, Owner: "intern", IdempotencyKey: winner, PayloadHash: winner, Answer: winner, ExpectedRevision: 1, AIRequest: json.RawMessage(`{"task_id":"DEMO-1","question":"answer"}`)})
	if e != nil {
		t.Fatalf("repeat answer: %v", e)
	}
	answers, e := s.Answers(ctx, "intern", a.ID)
	if e != nil || len(answers) != 1 {
		t.Fatalf("answers: %v %v", answers, e)
	}
	queued, e := s.GetAnalysis(ctx, "intern", a.ID, nil)
	if e != nil || queued.Status != "queued" || queued.ResultRevision == nil || *queued.ResultRevision != 1 {
		t.Fatalf("queued status: %+v %v", queued, e)
	}
	job, e = s.ClaimJob(ctx, time.Second*5, 2)
	if e != nil || job == nil {
		t.Fatalf("second claim: %v", e)
	}
	if e = s.FinishJob(ctx, *job, resultCompletion()); e != nil {
		t.Fatal(e)
	}
	current, e := s.GetAnalysis(ctx, "intern", a.ID, nil)
	if e != nil || current.ResultRevision == nil || *current.ResultRevision != 2 || len(current.StepProgress) != 0 {
		t.Fatalf("revision 2 progress: %+v %v", current, e)
	}
	one := 1
	old, e := s.GetAnalysis(ctx, "intern", a.ID, &one)
	if e != nil || len(old.StepProgress) != 1 || !old.StepProgress[0].Completed {
		t.Fatalf("old progress: %+v %v", old, e)
	}
	if _, e = s.SetProgress(ctx, "intern", a.ID, 1, "v1-s1", false); !errors.Is(e, ErrConflict) {
		t.Fatalf("old revision conflict: %v", e)
	}
}

// A delayed answer prepared from revision 1 must not be accepted after a faster
// request has produced revision 2, even when both results need clarification.
func TestIntegrationAnswerRejectsStaleRevision(t *testing.T) {
	s, _ := integrationStore(t)
	ctx := context.Background()
	a := makeAnalysis(t, s, "race-create")
	job, err := s.ClaimJob(ctx, 5*time.Second, 2)
	if err != nil || job == nil {
		t.Fatalf("first claim: %v", err)
	}
	clarification := resultCompletion()
	clarification.Status = "needs_clarification"
	clarification.Result = json.RawMessage(`{"task_id":"DEMO-1","status":"needs_clarification","plan":{"findings":[],"steps":[],"questions":["question"]},"sources":[],"jira_markdown":"draft"}`)
	if err = s.FinishJob(ctx, *job, clarification); err != nil {
		t.Fatal(err)
	}
	stale := AnswerInput{AnalysisID: a.ID, Owner: "intern", IdempotencyKey: "delayed", PayloadHash: "delayed", Answer: "old", ExpectedRevision: 1, AIRequest: json.RawMessage(`{"question":"old"}`)}
	fresh := AnswerInput{AnalysisID: a.ID, Owner: "intern", IdempotencyKey: "first", PayloadHash: "first", Answer: "first", ExpectedRevision: 1, AIRequest: json.RawMessage(`{"question":"first"}`)}
	if _, err = s.SubmitAnswer(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	job, err = s.ClaimJob(ctx, 5*time.Second, 2)
	if err != nil || job == nil {
		t.Fatalf("second claim: %v", err)
	}
	if err = s.FinishJob(ctx, *job, clarification); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SubmitAnswer(ctx, stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("delayed answer: wanted conflict, got %v", err)
	}
	current, err := s.GetAnalysis(ctx, "intern", a.ID, nil)
	if err != nil || current.Status != "needs_clarification" || current.ResultRevision == nil || *current.ResultRevision != 2 {
		t.Fatalf("result changed: %+v %v", current, err)
	}
	answers, err := s.Answers(ctx, "intern", a.ID)
	if err != nil || len(answers) != 1 || answers[0] != "first" {
		t.Fatalf("answer history: %v %v", answers, err)
	}
	if _, err = s.SubmitAnswer(ctx, fresh); err != nil {
		t.Fatalf("repeat of accepted operation: %v", err)
	}
	var active int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM analysis_jobs WHERE analysis_id=$1 AND state IN ('queued','running')`, a.ID).Scan(&active); err != nil || active != 0 {
		t.Fatalf("active jobs: %d %v", active, err)
	}
}
