package jobs

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/s3eetdev/AI-buddy/backend/internal/storage/postgres"
)

type Config struct {
	Concurrency      int
	Lease, Heartbeat time.Duration
	MaxAttempts      int
}
type Processor func(context.Context, postgres.Job) (postgres.Completion, error)
type Error struct {
	Code, Message string
	Retryable     bool
}

func (e *Error) Error() string { return e.Code }
func Run(ctx context.Context, store postgres.WorkerStore, process Processor, cfg Config) {
	var wg sync.WaitGroup
	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); worker(ctx, store, process, cfg) }()
	}
	wg.Wait()
}
func worker(ctx context.Context, store postgres.WorkerStore, process Processor, cfg Config) {
	for ctx.Err() == nil {
		j, e := store.ClaimJob(ctx, cfg.Lease, cfg.MaxAttempts)
		if e != nil {
			if ctx.Err() == nil {
				log.Print("worker: claim failed")
			}
			if !wait(ctx, time.Second) {
				return
			}
			continue
		}
		if j == nil {
			if !wait(ctx, time.Second) {
				return
			}
			continue
		}
		workctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		var hb sync.WaitGroup
		hb.Add(1)
		go func() {
			defer hb.Done()
			t := time.NewTicker(cfg.Heartbeat)
			defer t.Stop()
			for {
				select {
				case <-done:
					return
				case <-workctx.Done():
					return
				case <-t.C:
					if store.Heartbeat(workctx, *j, cfg.Lease) != nil {
						cancel()
						return
					}
				}
			}
		}()
		result, err := process(workctx, *j)
		close(done)
		hb.Wait()
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			if e = store.FinishJob(ctx, *j, result); e != nil && !errors.Is(e, postgres.ErrLeaseLost) {
				log.Print("worker: could not save result")
			}
			continue
		}
		f := &Error{Code: "analysis_failed", Message: "Analysis could not be completed"}
		var classified *Error
		if errors.As(err, &classified) {
			f = classified
		}
		if e = store.FailJob(ctx, *j, postgres.PublicError{Code: f.Code, Message: f.Message}, f.Retryable, time.Duration(j.Attempt)*time.Second, cfg.MaxAttempts); e != nil && !errors.Is(e, postgres.ErrLeaseLost) {
			log.Print("worker: could not save failure")
		}
	}
}
func wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
