package ai

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReplayRefusesUnrecordedOrTamperedContext(t *testing.T) {
	r, err := NewReplay("../../../../examples/recorded", "../../../../data/demo_tasks.json")
	if err != nil {
		t.Fatal(err)
	}
	req := r.issues["DEMO-1"]
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = r.Analyze(ctx, req); err == nil {
		t.Fatal("cancelled request accepted")
	}
	bad := req
	bad.Title = ""
	if err = r.Check(bad); err == nil {
		t.Fatal("invalid input accepted")
	}
	bad = req
	bad.Question = "new question"
	if _, err = r.Analyze(context.Background(), bad); err == nil {
		t.Fatal("unrecorded clarification accepted")
	}
	record := r.records[req.TaskID]
	delete(r.records, req.TaskID)
	if err = r.Check(req); err == nil {
		t.Fatal("missing record accepted")
	}
	r.records[req.TaskID] = record
	for i := range record.Sources {
		if record.Sources[i].ID == "task:"+req.TaskID {
			record.Sources[i].Content = "changed input"
		}
	}
	r.records[req.TaskID] = record
	if err = r.Check(req); err == nil {
		t.Fatal("tampered task snapshot accepted")
	}
	record.Sources = []Source{}
	r.records[req.TaskID] = record
	if err = r.Check(req); err == nil {
		t.Fatal("missing task snapshot accepted")
	}
}

func TestReplayRejectsMalformedFiles(t *testing.T) {
	dir := t.TempDir()
	seed := filepath.Join(dir, "seed.json")
	if _, err := NewReplay(dir, seed); err == nil {
		t.Fatal("missing seed accepted")
	}
	for _, value := range []string{"{", `{"issues":[{"task_id":"../secret"}]}`} {
		if err := os.WriteFile(seed, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewReplay(dir, seed); err == nil {
			t.Fatal("bad seed accepted")
		}
	}
	data, _ := json.Marshal(map[string]any{"issues": []Request{{TaskID: "DEMO-1"}}})
	if err := os.WriteFile(seed, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "DEMO-1.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewReplay(dir, seed); err == nil {
		t.Fatal("corrupt replay accepted")
	}
}
