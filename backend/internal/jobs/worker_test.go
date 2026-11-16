package jobs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/s3eetdev/AI-buddy/backend/internal/storage/postgres"
)

type workerStore struct {
	claimed                         bool
	claimErr, heartbeatErr, saveErr error
	finished                        chan postgres.Completion
	failed                          chan postgres.PublicError
	retryable                       bool
	mu                              sync.Mutex
}

func (s *workerStore) ClaimJob(context.Context, time.Duration, int) (*postgres.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	if s.claimed {
		return nil, nil
	}
	s.claimed = true
	return &postgres.Job{ID: "job", Attempt: 1}, nil
}
func (s *workerStore) Heartbeat(context.Context, postgres.Job, time.Duration) error {
	return s.heartbeatErr
}
func (s *workerStore) FinishJob(_ context.Context, _ postgres.Job, c postgres.Completion) error {
	s.finished <- c
	return s.saveErr
}
func (s *workerStore) FailJob(_ context.Context, _ postgres.Job, e postgres.PublicError, retryable bool, _ time.Duration, _ int) error {
	s.retryable = retryable
	s.failed <- e
	return s.saveErr
}
func TestWorkerCompletesAndClassifiesFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failure  error
		expected string
		retry    bool
		saveErr  error
	}{
		{"success", nil, "", false, nil},
		{"storage failure", nil, "", false, errors.New("storage failed")},
		{"lost lease", nil, "", false, postgres.ErrLeaseLost},
		{"unclassified", errors.New("private provider body"), "analysis_failed", false, nil},
		{"retryable", &Error{Code: "ai_unavailable", Message: "safe error", Retryable: true}, "ai_unavailable", true, nil},
		{"failure persistence", errors.New("provider error"), "analysis_failed", false, errors.New("storage failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := &workerStore{finished: make(chan postgres.Completion, 1), failed: make(chan postgres.PublicError, 1), saveErr: tc.saveErr}
			done := make(chan struct{})
			go func() {
				defer close(done)
				Run(ctx, s, func(context.Context, postgres.Job) (postgres.Completion, error) {
					return postgres.Completion{Status: "ready"}, tc.failure
				}, Config{Concurrency: 1, Heartbeat: time.Millisecond, Lease: time.Second, MaxAttempts: 2})
			}()
			if tc.failure == nil {
				select {
				case c := <-s.finished:
					if c.Status != "ready" {
						t.Fatal(c)
					}
				case <-time.After(time.Second):
					t.Fatal("not completed")
				}
			} else {
				select {
				case e := <-s.failed:
					if e.Code != tc.expected || s.retryable != tc.retry {
						t.Fatalf("wrong classification: %+v", e)
					}
				case <-time.After(time.Second):
					t.Fatal("not failed")
				}
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("worker did not stop")
			}
		})
	}
}
func TestHeartbeatLossCancelsProcessor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &workerStore{heartbeatErr: postgres.ErrLeaseLost, failed: make(chan postgres.PublicError, 1)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, s, func(work context.Context, _ postgres.Job) (postgres.Completion, error) {
			<-work.Done()
			return postgres.Completion{}, &Error{Code: "lease_lost", Message: "Cancelled"}
		}, Config{Concurrency: 1, Heartbeat: time.Millisecond, Lease: time.Second, MaxAttempts: 2})
	}()
	select {
	case e := <-s.failed:
		if e.Code != "lease_lost" {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("processor was not cancelled")
	}
	cancel()
	<-done
}
func TestClaimFailureAndEmptyQueueStopOnCancellation(t *testing.T) {
	for _, claimErr := range []error{nil, errors.New("database unavailable")} {
		ctx, cancel := context.WithCancel(context.Background())
		s := &workerStore{claimed: true, claimErr: claimErr}
		done := make(chan struct{})
		go func() { defer close(done); Run(ctx, s, nil, Config{Concurrency: 1}) }()
		time.Sleep(10 * time.Millisecond)
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("claim loop did not stop")
		}
	}
}
func TestWaitAndError(t *testing.T) {
	if !wait(context.Background(), time.Millisecond) {
		t.Fatal("timer did not fire")
	}
	if (&Error{Code: "safe"}).Error() != "safe" {
		t.Fatal("wrong error")
	}
}
