package postgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryThenTerminalFailureIsPersisted(t *testing.T) {
	s, _ := integrationStore(t)
	ctx := context.Background()
	a := makeAnalysis(t, s, "retry-terminal")
	j, err := s.ClaimJob(ctx, time.Second, 2)
	if err != nil || j == nil {
		t.Fatalf("claim: %v", err)
	}
	wrong := *j
	wrong.AnalysisID = "wrong"
	if err = s.FailJob(ctx, wrong, PublicError{Code: "safe"}, true, 0, 2); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("wrong owner: %v", err)
	}
	failure := PublicError{Code: "ai_unavailable", Message: "Safe failure"}
	if err = s.FailJob(ctx, *j, failure, true, 0, 2); err != nil {
		t.Fatal(err)
	}
	if err = s.FailJob(ctx, *j, failure, true, 0, 2); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale failure: %v", err)
	}
	current, err := s.GetAnalysis(ctx, "intern", a.ID, nil)
	if err != nil || current.Status != "queued" {
		t.Fatalf("not requeued: %+v %v", current, err)
	}
	j, err = s.ClaimJob(ctx, time.Second, 2)
	if err != nil || j == nil || j.Attempt != 2 {
		t.Fatalf("retry: %+v %v", j, err)
	}
	if err = s.Heartbeat(ctx, *j, time.Second); err != nil {
		t.Fatal(err)
	}
	if err = s.FailJob(ctx, *j, failure, true, 0, 2); err != nil {
		t.Fatal(err)
	}
	current, err = s.GetAnalysis(ctx, "intern", a.ID, nil)
	if err != nil || current.Status != "failed" || current.Error == nil || current.Error.Code != failure.Code {
		t.Fatalf("terminal failure: %+v %v", current, err)
	}
	if next, err := s.ClaimJob(ctx, time.Second, 2); err != nil || next != nil {
		t.Fatalf("exhausted job reclaimed: %+v %v", next, err)
	}
}

func TestNonretryableFailureAndReadiness(t *testing.T) {
	s, _ := integrationStore(t)
	ctx := context.Background()
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	a := makeAnalysis(t, s, "nonretryable")
	j, err := s.ClaimJob(ctx, time.Second, 3)
	if err != nil || j == nil {
		t.Fatalf("claim: %v", err)
	}
	if err = s.FailJob(ctx, *j, PublicError{Code: "ai_invalid_response", Message: "Invalid output"}, false, 0, 3); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListAnalyses(ctx, "intern", 20, 0)
	if err != nil || len(items) != 1 || items[0].ID != a.ID || items[0].Status != "failed" {
		t.Fatalf("list: %+v %v", items, err)
	}
	if _, err = s.Pool.Exec(ctx, "DROP TABLE step_progress"); err != nil {
		t.Fatal(err)
	}
	if err = s.Ready(ctx); err == nil {
		t.Fatal("missing migration was accepted")
	}
}
