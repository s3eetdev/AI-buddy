package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLiveNormalizesPythonRequestWithoutMutatingOriginal(t *testing.T) {
	original := Request{TaskID: " T ", Title: " Заголовок ", Description: "\tОписание\n", AcceptanceCriteria: []string{" критерий "}, AllowedScopes: []string{" demo "}, Question: " \n  первый ответ  \n  второй ответ  \n "}
	want := Request{TaskID: "T", Title: "Заголовок", Description: "Описание", AcceptanceCriteria: []string{"критерий"}, AllowedScopes: []string{"demo"}, Question: "первый ответ  \n  второй ответ"}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got Request
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("sent %+v, want %+v", got, want)
		}
		fmt.Fprint(w, `{"task_id":"T","status":"insufficient_context","plan":{"summary":"нет","findings":[],"steps":[],"questions":[],"limitations":[]},"sources":[],"model":null,"usage":{},"jira_markdown":""}`)
	}))
	defer s.Close()
	if _, err := NewLive(s.URL, "secret", time.Second).Analyze(context.Background(), original); err != nil {
		t.Fatal(err)
	}
	if original.Question != " \n  первый ответ  \n  второй ответ  \n " || original.AcceptanceCriteria[0] != " критерий " || original.AllowedScopes[0] != " demo " {
		t.Fatal("original request was modified")
	}
	// Pydantic core uses Unicode whitespace, and preserves information separators.
	if got := NormalizeRequest(Request{Question: "\u001cтекст\u001f"}).Question; got != "\u001cтекст\u001f" {
		t.Fatalf("unexpected trimming %q", got)
	}
}

func TestLiveContract(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/analyze" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("wrong request")
		}
		var m map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&m)
		if len(m) != 6 || string(m["allowed_scopes"]) != "[]" {
			t.Errorf("bad body %v", m)
		}
		fmt.Fprint(w, `{"task_id":"T","status":"insufficient_context","plan":{"summary":"нет","findings":[],"steps":[],"questions":[],"limitations":[]},"sources":[],"model":null,"usage":{},"jira_markdown":""}`)
	}))
	defer s.Close()
	_, err := NewLive(s.URL, "secret", time.Second).Analyze(context.Background(), Request{TaskID: "T", Title: "Тест"})
	if err != nil {
		t.Fatal(err)
	}
}
func TestErrorsSafe(t *testing.T) {
	for _, tt := range []struct {
		status     int
		body, code string
		retry      bool
	}{{401, `{"detail":"secret"}`, "ai_unauthorized", false}, {422, `{"detail":[]}`, "ai_request_rejected", false}, {502, `{"error":{"code":"llm_ungrounded_response"}}`, "ai_invalid_response", false}, {502, `{"error":{"code":"llm_invalid_response"}}`, "ai_invalid_response", false}, {503, `{"error":{"code":"llm_not_configured"}}`, "ai_invalid_response", false}, {503, `{"detail":"service_token_not_configured"}`, "ai_invalid_response", false}, {502, "secret traceback", "ai_unavailable", true}, {504, "oops", "ai_unavailable", true}, {200, "not json", "ai_invalid_response", false}, {200, strings.Repeat("x", (4<<20)+1), "ai_response_too_large", false}} {
		t.Run(fmt.Sprint(tt.status, tt.code), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tt.status); fmt.Fprint(w, tt.body) }))
			defer s.Close()
			_, err := NewLive(s.URL, "secret", time.Second).Analyze(context.Background(), Request{TaskID: "a", Title: "b"})
			ae, ok := err.(*Error)
			if !ok || ae.Code != tt.code || ae.Retryable != tt.retry || strings.Contains(err.Error(), "secret") {
				t.Fatalf("%v", err)
			}
		})
	}
}
func TestUnicodeAndReplay(t *testing.T) {
	q := Request{TaskID: "a", Title: strings.Repeat("я", 500)}
	if ValidateInput(q) != nil {
		t.Fatal("runes counted as bytes")
	}
	q.Title += "я"
	if ValidateInput(q) == nil {
		t.Fatal("too long")
	}
	r, e := NewReplay("../../../../examples/recorded", "../../../../data/demo_tasks.json")
	if e != nil {
		t.Fatal(e)
	}
	req := r.issues["DEMO-5"]
	if e = r.Check(req); e != nil {
		t.Fatal(e)
	}
	req.AllowedScopes = []string{"demo"}
	if r.Check(req) == nil {
		t.Fatal("private leak")
	}
	req.Question = "ответ"
	if r.Check(req) == nil {
		t.Fatal("new answer accepted")
	}
	req.Question = ""
	req.AllowedScopes = []string{}
	got, e := r.Analyze(context.Background(), req)
	if e != nil || got.Status != "insufficient_context" || len(got.Sources) != 0 {
		t.Fatalf("%+v %v", got, e)
	}
}
