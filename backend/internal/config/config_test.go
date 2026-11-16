package config

import "testing"

func cleanEnv(t *testing.T) {

	t.Helper()
	for _, k := range []string{"HTTP_ADDR", "DATABASE_URL", "AI_MODE", "AI_BASE_URL", "AI_SERVICE_TOKEN", "AUTH_MODE", "DEMO_PROFILE", "JIRA_BASE_URL", "SEED_PATH", "CORPUS_PATH", "REPLAY_DIR", "CORS_ORIGINS", "AI_REQUEST_TIMEOUT", "JOB_LEASE", "JOB_HEARTBEAT", "WORKER_CONCURRENCY", "JOB_MAX_ATTEMPTS"} {
		t.Setenv(k, "")
	}
}

func TestDefaultsAndLiveConfiguration(t *testing.T) {
	cleanEnv(t)
	c, err := Load()
	if err != nil || c.AIMode != "replay" || c.Profile != "intern" || c.WorkerConcurrency != 1 {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	t.Setenv("AI_MODE", "live")
	t.Setenv("AI_SERVICE_TOKEN", "test-service-token")
	t.Setenv("CORS_ORIGINS", "https://demo.example, http://localhost:5173")
	c, err = Load()
	if err != nil || len(c.CORSOrigins) != 2 || c.AIMode != "live" {
		t.Fatalf("live configuration: %+v %v", c, err)
	}
}

func TestRejectUnsafeConfiguration(t *testing.T) {
	cases := []struct{ key, value string }{
		{"AI_REQUEST_TIMEOUT", "bad"}, {"JOB_LEASE", "bad"}, {"JOB_HEARTBEAT", "bad"},
		{"WORKER_CONCURRENCY", "bad"}, {"JOB_MAX_ATTEMPTS", "bad"},
		{"AUTH_MODE", "untrusted"}, {"AI_MODE", "unknown"}, {"AI_MODE", "live"},
		{"WORKER_CONCURRENCY", "0"}, {"WORKER_CONCURRENCY", "65"}, {"JOB_MAX_ATTEMPTS", "0"},
		{"AI_REQUEST_TIMEOUT", "0s"}, {"JOB_HEARTBEAT", "0s"}, {"JOB_LEASE", "30s"},
		{"CORS_ORIGINS", "*"}, {"CORS_ORIGINS", "https://user:pass@demo.example"},
		{"CORS_ORIGINS", "https://demo.example/path"}, {"CORS_ORIGINS", "https://demo.example?x=1"},
		{"CORS_ORIGINS", "https://demo.example#fragment"},
		{"AI_BASE_URL", "file:///tmp/ai"}, {"AI_BASE_URL", "https://user:pass@ai.example"},
		{"JIRA_BASE_URL", "https://jira.example/browse/DEMO-1"}, {"JIRA_BASE_URL", "%"},
	}
	for _, tc := range cases {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			cleanEnv(t)
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil {
				t.Fatal("accepted unsafe or invalid configuration")
			}
		})
	}
}
