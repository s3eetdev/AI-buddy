package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"syscall"
	"time"

	"github.com/s3eetdev/AI-buddy/backend/internal/config"
	"github.com/s3eetdev/AI-buddy/backend/internal/evidence"
	"github.com/s3eetdev/AI-buddy/backend/internal/httpapi"
	"github.com/s3eetdev/AI-buddy/backend/internal/integrations/ai"
	"github.com/s3eetdev/AI-buddy/backend/internal/integrations/jira"
	"github.com/s3eetdev/AI-buddy/backend/internal/jobs"
	"github.com/s3eetdev/AI-buddy/backend/internal/storage/postgres"
)

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	seed, err := jira.Load(cfg.SeedPath)
	if err != nil {
		return err
	}
	if _, ok := seed.Profile(cfg.Profile); !ok {
		return errors.New("unknown demo profile")
	}
	corpus, err := evidence.Load(cfg.CorpusPath)
	if err != nil {
		return err
	}
	var client ai.Client
	var replay *ai.Replay
	if cfg.AIMode == "replay" {
		replay, err = ai.NewReplay(cfg.ReplayDir, cfg.SeedPath)
		if err != nil {
			return err
		}
		client = replay
	} else {
		client = ai.NewLive(cfg.AIBaseURL, cfg.AIToken, cfg.AIRequestTimeout)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	store, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		return err
	}
	processor := func(ctx context.Context, job postgres.Job) (postgres.Completion, error) {
		var req ai.Request
		if json.Unmarshal(job.Input, &req) != nil || ai.ValidateInput(req) != nil {
			return postgres.Completion{}, &jobs.Error{Code: "invalid_ai_request", Message: "Некорректный запрос анализа"}
		}
		issue, ok := seed.Issue(req.TaskID)
		profile, pok := seed.Profile(job.Owner)
		if !ok || !pok || !reflect.DeepEqual(jira.Scopes(issue, profile), req.AllowedScopes) {
			return postgres.Completion{}, &jobs.Error{Code: "source_access_denied", Message: "Права на источники изменились"}
		}
		result, e := client.Analyze(ctx, req)
		if e != nil {
			var ae *ai.Error
			if errors.As(e, &ae) {
				return postgres.Completion{}, &jobs.Error{Code: ae.Code, Message: "Не удалось выполнить анализ", Retryable: ae.Retryable}
			}
			return postgres.Completion{}, &jobs.Error{Code: "ai_error", Message: "Не удалось выполнить анализ"}
		}
		if e = corpus.Validate(req, result); e != nil {
			return postgres.Completion{}, &jobs.Error{Code: "invalid_ai_evidence", Message: "Ответ AI не прошёл проверку источников"}
		}
		raw, e := json.Marshal(result)
		if e != nil {
			return postgres.Completion{}, &jobs.Error{Code: "invalid_ai_response", Message: "Некорректный ответ AI"}
		}
		return postgres.Completion{Result: raw, AIRequest: job.Input, CorpusDigest: corpus.Digest(), Status: result.Status, ExecutionMode: job.ExecutionMode}, nil
	}
	handler := (&httpapi.Server{Config: cfg, Store: store, Seed: seed, Corpus: corpus, Replay: replay}).Handler()
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		jobs.Run(ctx, store, processor, jobs.Config{Concurrency: cfg.WorkerConcurrency, Lease: cfg.JobLease, Heartbeat: cfg.JobHeartbeat, MaxAttempts: cfg.JobMaxAttempts})
	}()
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()
	log.Printf("backend listening on %s (mode %s, demo profile %s)", cfg.HTTPAddr, cfg.AIMode, cfg.Profile)
	select {
	case <-ctx.Done():
	case err = <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			stop()
		}
	}
	stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	<-workerDone
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		log.Printf("backend failed: %v", err)
		os.Exit(1)
	}
}
