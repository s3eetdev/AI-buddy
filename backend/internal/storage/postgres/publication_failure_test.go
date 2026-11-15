package postgres

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIntegrationFailedPublicationPersistsAndRetries(t *testing.T) {
	s, cfg := integrationStore(t)
	ctx := context.Background()
	a := makeAnalysis(t, s, "publication-failure-create")
	job, err := s.ClaimJob(ctx, 5*time.Second, 2)
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	if err = s.FinishJob(ctx, *job, resultCompletion()); err != nil {
		t.Fatal(err)
	}
	// Simulate a write-side exception containing sensitive database details.
	_, err = s.Pool.Exec(ctx, `CREATE FUNCTION reject_mock_plan() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'SECRET-INTERNAL-DATABASE-ERROR'; END $$`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool.Exec(ctx, `CREATE TRIGGER reject_mock_plan BEFORE INSERT ON mock_jira_plans FOR EACH ROW EXECUTE FUNCTION reject_mock_plan()`); err != nil {
		t.Fatal(err)
	}
	in := PublishInput{Owner: "intern", AnalysisID: a.ID, Revision: 1, IdempotencyKey: "publish-failure", PayloadHash: "same"}
	if _, err = s.Publish(ctx, in); !errors.Is(err, ErrPublishFailed) || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("unsafe failure: %v", err)
	}
	var count int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM mock_jira_plans WHERE analysis_id=$1`, a.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("plan after failed transaction: count=%d err=%v", count, err)
	}
	// A new pool emulates a process restart and verifies durable state.
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	reopened := &Store{Pool: pool}
	got, err := reopened.GetAnalysis(ctx, "intern", a.ID, nil)
	if err != nil || got.Publication.Status != "publish_failed" || got.Publication.Error == nil || got.Publication.Error.Code != "publish_failed" || strings.Contains(got.Publication.Error.Message, "SECRET") {
		t.Fatalf("durable failure: %+v %v", got, err)
	}
	if _, err = reopened.GetAnalysis(ctx, "reviewer", a.ID, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failure leaked to another profile: %v", err)
	}
	wrong := in
	wrong.PayloadHash = "different"
	if _, err = reopened.Publish(ctx, wrong); !errors.Is(err, ErrConflict) {
		t.Fatalf("key payload conflict: %v", err)
	}
	other := in
	other.IdempotencyKey = "second-failing-key"
	if _, err = reopened.Publish(ctx, other); !errors.Is(err, ErrPublishFailed) {
		t.Fatalf("second failure: %v", err)
	}
	got, err = reopened.GetAnalysis(ctx, "intern", a.ID, nil)
	if err != nil || got.Publication.Status != "publish_failed" || got.Publication.Error == nil {
		t.Fatalf("state after second failure: %+v %v", got, err)
	}
	if _, err = s.Pool.Exec(ctx, `DROP TRIGGER reject_mock_plan ON mock_jira_plans`); err != nil {
		t.Fatal(err)
	}
	const concurrent = 8
	var wg sync.WaitGroup
	plans := make(chan string, concurrent)
	errs := make(chan error, concurrent)
	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, e := reopened.Publish(ctx, in)
			if e == nil {
				plans <- p.ID
			}
			errs <- e
		}()
	}
	wg.Wait()
	close(plans)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatalf("retry: %v", e)
		}
	}
	id := ""
	for p := range plans {
		if id != "" && p != id {
			t.Fatal("retry produced duplicate plans")
		}
		id = p
	}
	if id == "" {
		t.Fatal("no published plan")
	}
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM mock_jira_plans WHERE analysis_id=$1`, a.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("plan count=%d err=%v", count, err)
	}
	got, err = reopened.GetAnalysis(ctx, "intern", a.ID, nil)
	if err != nil || got.Publication.Status != "published" || got.Publication.PlanID != id || got.Publication.Error != nil {
		t.Fatalf("published status: %+v %v", got, err)
	}
	// A failed receipt under a different key must resolve to the already committed plan.
	p, err := reopened.Publish(ctx, other)
	if err != nil || p.ID != id {
		t.Fatalf("different failed key retry: %+v %v", p, err)
	}
}
