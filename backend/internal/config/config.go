package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr, DatabaseURL, AIMode, AIBaseURL, AIToken, AuthMode, Profile, JiraBaseURL string
	SeedPath, CorpusPath, ReplayDir                                                   string
	CORSOrigins                                                                       []string
	AIRequestTimeout, JobLease, JobHeartbeat                                          time.Duration
	WorkerConcurrency, JobMaxAttempts                                                 int
}

func Load() (Config, error) {
	c := Config{HTTPAddr: env("HTTP_ADDR", "127.0.0.1:8080"), DatabaseURL: env("DATABASE_URL", "postgres://navigator:navigator@127.0.0.1:5433/navigator?sslmode=disable"), AIMode: env("AI_MODE", "replay"), AIBaseURL: env("AI_BASE_URL", "http://127.0.0.1:8001"), AIToken: os.Getenv("AI_SERVICE_TOKEN"), AuthMode: env("AUTH_MODE", "demo"), Profile: env("DEMO_PROFILE", "intern"), JiraBaseURL: env("JIRA_BASE_URL", "https://demo-jira.local"), SeedPath: env("SEED_PATH", "../data/demo_tasks.json"), CorpusPath: env("CORPUS_PATH", "../data/demo_sources.json"), ReplayDir: env("REPLAY_DIR", "../examples/recorded")}
	var err error
	if c.AIRequestTimeout, err = duration("AI_REQUEST_TIMEOUT", "310s"); err != nil {
		return c, err
	}
	if c.JobLease, err = duration("JOB_LEASE", "60s"); err != nil {
		return c, err
	}
	if c.JobHeartbeat, err = duration("JOB_HEARTBEAT", "15s"); err != nil {
		return c, err
	}
	if c.WorkerConcurrency, err = integer("WORKER_CONCURRENCY", "1"); err != nil {
		return c, err
	}
	if c.JobMaxAttempts, err = integer("JOB_MAX_ATTEMPTS", "2"); err != nil {
		return c, err
	}
	if s := os.Getenv("CORS_ORIGINS"); s != "" {
		for _, origin := range strings.Split(s, ",") {
			origin = strings.TrimSpace(origin)
			u, e := url.Parse(origin)
			if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
				return c, fmt.Errorf("invalid CORS origin")
			}
			c.CORSOrigins = append(c.CORSOrigins, origin)
		}
	}
	if c.AuthMode != "demo" {
		return c, errors.New("AUTH_MODE must be demo")
	}
	if c.AIMode != "replay" && c.AIMode != "live" {
		return c, errors.New("AI_MODE must be replay or live")
	}
	if c.AIMode == "live" && c.AIToken == "" {
		return c, errors.New("AI_SERVICE_TOKEN is required in live mode")
	}
	if c.AIRequestTimeout <= 0 || c.JobHeartbeat <= 0 || c.JobLease <= c.JobHeartbeat*2 || c.WorkerConcurrency < 1 || c.WorkerConcurrency > 64 || c.JobMaxAttempts < 1 {
		return c, errors.New("invalid worker timing or limits")
	}
	for _, s := range []string{c.JiraBaseURL, c.AIBaseURL} {
		u, e := url.Parse(s)
		if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (s == c.JiraBaseURL && u.Path != "" && u.Path != "/") {
			return c, errors.New("invalid base URL")
		}
	}
	return c, nil
}
func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
func duration(k, d string) (time.Duration, error) {
	v, e := time.ParseDuration(env(k, d))
	if e != nil {
		return 0, fmt.Errorf("invalid %s", k)
	}
	return v, nil
}
func integer(k, d string) (int, error) {
	v, e := strconv.Atoi(env(k, d))
	if e != nil {
		return 0, fmt.Errorf("invalid %s", k)
	}
	return v, nil
}
