package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/s3eetdev/AI-buddy/backend/internal/integrations/ai"
	"github.com/s3eetdev/AI-buddy/backend/internal/jobs"
	"github.com/s3eetdev/AI-buddy/backend/internal/storage/postgres"
)

func TestClarificationWhitespaceAndRawHistory(t *testing.T) {
	store := testStore(t)
	server, corpus := setup(t, store)
	raw, err := os.ReadFile("../../../examples/recorded/DEMO-2.json")
	if err != nil {
		t.Fatal(err)
	}
	var recorded ai.Response
	if err = json.Unmarshal(raw, &recorded); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var requests []ai.Request
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request ai.Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		requests = append(requests, request)
		n := len(requests)
		mu.Unlock()
		// Simulate the Python model's stripping of the entire question, then its
		// virtual source construction; spaces inside the history stay intact.
		request.Question = strings.TrimSpace(request.Question)
		var response ai.Response
		if err := json.Unmarshal(raw, &response); err != nil {
			t.Error(err)
			return
		}
		if n >= 3 {
			response.Status = "ready"
			response.Plan.Questions = []string{}
		}
		for i := range response.Sources {
			if response.Sources[i].ID == "task:"+request.TaskID {
				response.Sources[i].Content = ai.TaskSourceContent(request)
			}
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer fake.Close()
	client := ai.NewLive(fake.URL, "test-token", 3*time.Second)
	process := func(ctx context.Context, job postgres.Job) (postgres.Completion, error) {
		var request ai.Request
		if err := json.Unmarshal(job.Input, &request); err != nil {
			return postgres.Completion{}, err
		}
		response, err := client.Analyze(ctx, request)
		if err != nil {
			return postgres.Completion{}, err
		}
		if err = corpus.Validate(request, response); err != nil {
			return postgres.Completion{}, err
		}
		result, err := json.Marshal(response)
		return postgres.Completion{Result: result, AIRequest: job.Input, CorpusDigest: corpus.Digest(), Status: response.Status, ExecutionMode: job.ExecutionMode}, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		jobs.Run(ctx, store, process, jobs.Config{Concurrency: 1, Lease: 2 * time.Second, Heartbeat: 200 * time.Millisecond, MaxAttempts: 2})
		close(done)
	}()
	defer func() { cancel(); <-done }()
	api := httptest.NewServer(server.Handler())
	defer api.Close()
	code, m := call(t, api.URL, "POST", "/api/v1/analyses", "spaces-create", `{"jira_url":"https://demo-jira.local/browse/DEMO-2"}`)
	expect(t, code, 202, m)
	id := m["analysis_id"].(string)
	waitStatus(t, api.URL, id, "needs_clarification")
	answer1 := " \n  Первый ответ  \n "
	answer2 := " \tВторой ответ  \n "
	body := func(answer string) string {
		b, e := json.Marshal(map[string]string{"answer": answer})
		if e != nil {
			t.Fatal(e)
		}
		return string(b)
	}
	code, m = call(t, api.URL, "POST", "/api/v1/analyses/"+id+"/answers", "spaces-answer-1", body(answer1))
	expect(t, code, 202, m)
	m = waitStatus(t, api.URL, id, "needs_clarification")
	if m["result_revision"] != float64(2) {
		t.Fatalf("expected second revision: %v", m)
	}
	// The 2000-character bound applies to the complete raw history before
	// normalization, even if the overrun is made up of removable whitespace.
	code, m = call(t, api.URL, "POST", "/api/v1/analyses/"+id+"/answers", "spaces-too-long", body("текст"+strings.Repeat(" ", 2000)))
	expect(t, code, 422, m)
	if m["error"].(map[string]any)["code"] != "question_too_long" {
		t.Fatalf("wrong limit error: %v", m)
	}
	code, m = call(t, api.URL, "POST", "/api/v1/analyses/"+id+"/answers", "spaces-answer-2", body(answer2))
	expect(t, code, 202, m)
	m = waitStatus(t, api.URL, id, "ready")
	if m["result_revision"] != float64(3) {
		t.Fatalf("expected third revision: %v", m)
	}
	answers, err := store.Answers(context.Background(), "intern", id)
	if err != nil || !reflect.DeepEqual(answers, []string{answer1, answer2}) {
		t.Fatalf("raw answers = %q, %v", answers, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 3 {
		t.Fatalf("requests: %+v", requests)
	}
	if requests[1].Question != strings.TrimSpace(answer1) {
		t.Fatalf("first question %q", requests[1].Question)
	}
	if requests[2].Question != strings.TrimSpace(answer1+"\n"+answer2) {
		t.Fatalf("accumulated question %q", requests[2].Question)
	}
	var stored ai.Request
	a, err := store.GetAnalysis(context.Background(), "intern", id, nil)
	if err != nil || json.Unmarshal(a.AIRequest, &stored) != nil || stored.Question != requests[2].Question {
		t.Fatalf("stored input: %s (%v)", a.AIRequest, err)
	}
}
