package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func serverEnv(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{
		"AUTH_MODE": "demo", "DEMO_PROFILE": "intern", "AI_MODE": "replay",
		"SEED_PATH": "../../../data/demo_tasks.json", "CORPUS_PATH": "../../../data/demo_sources.json",
		"REPLAY_DIR": "../../../examples/recorded", "CORS_ORIGINS": "",
		"AI_BASE_URL": "http://127.0.0.1:8001", "JIRA_BASE_URL": "https://demo-jira.local",
		"AI_REQUEST_TIMEOUT": "2s", "JOB_HEARTBEAT": "10ms", "JOB_LEASE": "1s",
		"WORKER_CONCURRENCY": "1", "JOB_MAX_ATTEMPTS": "1",
	} {
		t.Setenv(k, v)
	}
}

func TestStartupRejectsInvalidResources(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"AUTH_MODE", "invalid"}, {"SEED_PATH", "missing.json"}, {"DEMO_PROFILE", "unknown"},
		{"CORPUS_PATH", "missing.json"}, {"DATABASE_URL", "%"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			serverEnv(t)
			t.Setenv(tc.key, tc.value)
			if err := run(); err == nil {
				t.Fatal("bad startup accepted")
			}
		})
	}
}

// Exercise the real entrypoint, queue, processor and HTTP server against an isolated
// PostgreSQL schema. No provider call is made: live uses a local HTTP stub.
func TestServerLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix signal lifecycle: use compose.coverage.yaml on Windows")
	}
	addr := os.Getenv("TEST_DATABASE_URL")
	if addr == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	for _, mode := range []string{"replay", "live"} {
		t.Run(mode, func(t *testing.T) {
			serverEnv(t)
			ctx := context.Background()
			admin, err := pgxpool.New(ctx, addr)
			if err != nil {
				t.Fatal(err)
			}
			defer admin.Close()
			schema := fmt.Sprintf("test_entrypoint_%d", time.Now().UnixNano())
			if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
				t.Fatal(err)
			}
			defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
			u, err := url.Parse(addr)
			if err != nil {
				t.Fatal(err)
			}
			q := u.Query()
			q.Set("search_path", schema+",public")
			u.RawQuery = q.Encode()
			t.Setenv("DATABASE_URL", u.String())
			t.Setenv("AI_MODE", mode)
			if mode == "live" {
				recorded, err := os.ReadFile("../../../examples/recorded/DEMO-1.json")
				if err != nil {
					t.Fatal(err)
				}
				stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer test-token" {
						t.Error("service token missing")
					}
					w.Header().Set("Content-Type", "application/json")
					w.Write(recorded)
				}))
				defer stub.Close()
				t.Setenv("AI_BASE_URL", stub.URL)
				t.Setenv("AI_SERVICE_TOKEN", "test-token")
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			httpAddr := listener.Addr().String()
			listener.Close()
			t.Setenv("HTTP_ADDR", httpAddr)
			done := make(chan error, 1)
			go func() { done <- run() }()
			base := "http://" + httpAddr
			client := http.Client{Timeout: time.Second}
			started := false
			for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
				res, e := client.Get(base + "/ready")
				if e == nil {
					res.Body.Close()
					if res.StatusCode == 200 {
						started = true
						break
					}
				}
				select {
				case e := <-done:
					t.Fatalf("server exited: %v", e)
				default:
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !started {
				t.Fatal("server never became ready")
			}
			defer func() {
				p, _ := os.FindProcess(os.Getpid())
				if e := p.Signal(syscall.SIGTERM); e != nil {
					t.Error(e)
				}
				select {
				case e := <-done:
					if e != nil {
						t.Error(e)
					}
				case <-time.After(5 * time.Second):
					t.Error("server did not shut down")
				}
			}()
			req, _ := http.NewRequest("POST", base+"/api/v1/analyses", bytes.NewBufferString(`{"jira_url":"https://demo-jira.local/browse/DEMO-1"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "entrypoint-test")
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			var created struct {
				ID string `json:"analysis_id"`
			}
			json.NewDecoder(res.Body).Decode(&created)
			res.Body.Close()
			if res.StatusCode != 202 || created.ID == "" {
				t.Fatalf("create: %d %+v", res.StatusCode, created)
			}
			completed := false
			for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
				res, err = client.Get(base + "/api/v1/analyses/" + created.ID)
				if err != nil {
					t.Fatal(err)
				}
				var result struct {
					Status string `json:"status"`
				}
				json.NewDecoder(res.Body).Decode(&result)
				res.Body.Close()
				if result.Status == "ready" {
					completed = true
					break
				}
				if result.Status == "failed" {
					t.Fatal("analysis failed")
				}
				time.Sleep(30 * time.Millisecond)
			}
			if !completed {
				t.Fatal("processor did not produce a result")
			}
			for _, path := range []string{"/api/v1/analyses", "/api/v1/me", "/api/v1/demo/issues"} {
				res, err = client.Get(base + path)
				if err != nil {
					t.Fatal(err)
				}
				io.Copy(io.Discard, res.Body)
				res.Body.Close()
				if res.StatusCode != 200 {
					t.Errorf("%s: %d", path, res.StatusCode)
				}
			}
		})
	}
}

func TestStartupInvalidCorpus(t *testing.T) {
	serverEnv(t)
	p := filepath.Join(t.TempDir(), "corpus.json")
	if err := os.WriteFile(p, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CORPUS_PATH", p)
	if err := run(); err == nil {
		t.Fatal("invalid corpus accepted")
	}
}

func TestStartupRejectsCorruptReplay(t *testing.T) {
	serverEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "DEMO-1.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REPLAY_DIR", dir)
	if err := run(); err == nil {
		t.Fatal("corrupt replay was accepted")
	}
}
