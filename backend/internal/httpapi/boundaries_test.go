package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPInputAndCORSBoundaries(t *testing.T) {
	store := testStore(t)
	server, _ := setup(t, store)
	server.Config.CORSOrigins = []string{"https://allowed.example"}
	api := httptest.NewServer(server.Handler())
	defer api.Close()
	for _, tc := range []struct {
		method, path, key, body string
		status                  int
	}{
		{"GET", "/health", "", "", 200}, {"GET", "/missing", "", "", 404},
		{"GET", "/api/v1/analyses?limit=0", "", "", 400}, {"GET", "/api/v1/analyses?limit=abc", "", "", 400},
		{"GET", "/api/v1/analyses?offset=-1", "", "", 400}, {"GET", "/api/v1/analyses?offset=abc", "", "", 400},
		{"GET", "/api/v1/analyses?limit=10&offset=0", "", "", 200},
		{"POST", "/api/v1/analyses", "", `{"jira_url":"https://demo-jira.local/browse/DEMO-1"}`, 400},
		{"POST", "/api/v1/analyses", strings.Repeat("a", 201), `{}`, 400},
		{"POST", "/api/v1/analyses", "bad-url", `{"jira_url":"https://evil.local/browse/DEMO-1"}`, 422},
		{"POST", "/api/v1/analyses", "unknown-task", `{"jira_url":"https://demo-jira.local/browse/DEMO-999"}`, 404},
		{"POST", "/api/v1/analyses", "extra-json", `{} {}`, 400},
		{"POST", "/api/v1/analyses", "unknown-field", `{"secret":"bad"}`, 400},
		{"PATCH", "/api/v1/analyses/missing/steps/v1-s1", "", `{}`, 422},
		{"PATCH", "/api/v1/analyses/missing/steps/v1-s1", "", `{"result_revision":1,"completed":true}`, 404},
		{"POST", "/api/v1/analyses/missing/answers", "", `{"answer":"Email"}`, 400},
	} {
		code, m := call(t, api.URL, tc.method, tc.path, tc.key, tc.body)
		expect(t, code, tc.status, m)
	}
	for _, origin := range []string{"https://allowed.example", "https://evil.example"} {
		req, _ := http.NewRequest("OPTIONS", api.URL+"/api/v1/analyses", nil)
		req.Header.Set("Origin", origin)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 204 {
			t.Fatal("preflight failed")
		}
		allowed := res.Header.Get("Access-Control-Allow-Origin")
		if origin == "https://allowed.example" && allowed != origin {
			t.Fatal("allowed origin rejected")
		}
		if origin != "https://allowed.example" && allowed != "" {
			t.Fatal("untrusted origin allowed")
		}
	}
	server.Config.Profile = "unknown"
	code, m := call(t, api.URL, "GET", "/ready", "", "")
	expect(t, code, 503, m)
}
