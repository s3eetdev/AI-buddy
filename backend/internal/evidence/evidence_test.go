package evidence

import (
	"context"
	"encoding/json"
	"github.com/s3eetdev/AI-buddy/backend/internal/integrations/ai"
	"os"
	"reflect"
	"testing"
)

func TestSplitLines(t *testing.T) {
	cases := map[string][]string{"": {}, "а\r\nб\n": {"а", "б"}, "а\n\n": {"а", ""}, "а\u2028б\rв": {"а", "б", "в"}}
	for s, w := range cases {
		if g := SplitLines(s); !reflect.DeepEqual(g, w) {
			t.Errorf("%q = %#v", s, g)
		}
	}
}
func TestRecordedAndMutations(t *testing.T) {
	c, e := Load("../../../data/demo_sources.json")
	if e != nil {
		t.Fatal(e)
	}
	r, e := ai.NewReplay("../../../examples/recorded", "../../../data/demo_tasks.json")
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile("../../../data/demo_tasks.json")
	if e != nil {
		t.Fatal(e)
	}
	var seed struct {
		Issues []ai.Request `json:"issues"`
	}
	_ = json.Unmarshal(b, &seed)
	for _, req := range seed.Issues {
		res, e := r.Analyze(context.Background(), req)
		if e != nil {
			t.Fatalf("%s %v", req.TaskID, e)
		}
		if e = c.Validate(req, res); e != nil {
			t.Errorf("%s %v", req.TaskID, e)
		}
	}
	req := seed.Issues[0]
	res, _ := r.Analyze(context.Background(), req)
	clone := func() ai.Response { b, _ := json.Marshal(res); var v ai.Response; _ = json.Unmarshal(b, &v); return v }
	tests := map[string]func(*ai.Response){"task": func(v *ai.Response) { v.TaskID = "wrong" }, "duplicate": func(v *ai.Response) { v.Sources = append(v.Sources, v.Sources[0]) }, "private": func(v *ai.Response) { v.Sources[0].Scope = "legacy" }, "content": func(v *ai.Response) { v.Sources[0].Content += " " }, "line": func(v *ai.Response) { v.Plan.Findings[0].Citations[0].StartLine = 0 }, "quote": func(v *ai.Response) { v.Plan.Findings[0].Citations[0].Quote = "выдумано" }, "step": func(v *ai.Response) { v.Plan.Steps[0].SourceIDs = []string{"unknown"} }, "fact": func(v *ai.Response) { v.Plan.Findings[0].Citations = nil }, "virtual": func(v *ai.Response) {
		for i := range v.Sources {
			if v.Sources[i].ID == "task:"+req.TaskID {
				v.Sources[i].Content += " "
			}
		}
	}}
	for n, f := range tests {
		t.Run(n, func(t *testing.T) {
			v := clone()
			f(&v)
			if c.Validate(req, v) == nil {
				t.Fatal("accepted invalid evidence")
			}
		})
	}
}
func TestMultilineWhitespace(t *testing.T) {
	p := t.TempDir() + "/sources.json"
	content := " первая\r\nвторая \r\n"
	data, _ := json.Marshal([]ai.Source{{ID: "s", Scope: "demo", Path: "p", Kind: "code", Content: content}})
	_ = os.WriteFile(p, data, 0600)
	c, e := Load(p)
	if e != nil {
		t.Fatal(e)
	}
	req := ai.Request{TaskID: "T", Title: "Title", AllowedScopes: []string{"demo"}}
	res := ai.Response{TaskID: "T", Status: "ready", Plan: ai.Plan{Summary: "S", Findings: []ai.Finding{{Kind: "fact", Role: "context", Explanation: "E", Citations: []ai.Citation{{SourceID: "s", StartLine: 1, EndLine: 2, Quote: "первая\nвторая"}}}}, Steps: []ai.Step{{Action: "A", ExpectedResult: "E", SourceIDs: []string{"s"}}}, Questions: []string{}, Limitations: []string{}}, Sources: []ai.Source{{ID: "s", Scope: "demo", Path: "p", Kind: "code", Content: content}}, Usage: map[string]int{}}
	if e = c.Validate(req, res); e != nil {
		t.Fatal(e)
	}
	res.Sources[0].Content = "первая\r\nвторая\r\n"
	if c.Validate(req, res) == nil {
		t.Fatal("lost source whitespace")
	}
}

func TestVirtualTaskUsesPythonNormalizedQuestion(t *testing.T) {
	path := t.TempDir() + "/sources.json"
	if err := os.WriteFile(path, []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	corpus, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	req := ai.Request{TaskID: "DEMO-1", Title: " Заголовок ", Description: " Описание ", AcceptanceCriteria: []string{" Критерий "}, AllowedScopes: []string{"demo"}, Question: " \n  Ответ 1  \n  Ответ 2  \n "}
	content := "Задача: DEMO-1\nЗаголовок\nОписание\nКритерии приёмки:\nКритерий\nУточнение пользователя:\nОтвет 1  \n  Ответ 2"
	res := ai.Response{TaskID: "DEMO-1", Status: "ready", Plan: ai.Plan{Summary: "План", Findings: []ai.Finding{{Kind: "fact", Role: "context", Explanation: "Контекст", Citations: []ai.Citation{{SourceID: "task:DEMO-1", StartLine: 7, EndLine: 8, Quote: "Ответ 1  \n  Ответ 2"}}}}, Steps: []ai.Step{{Action: "Изучить", ExpectedResult: "Результат", SourceIDs: []string{"task:DEMO-1"}}}, Questions: []string{}, Limitations: []string{}}, Sources: []ai.Source{{ID: "task:DEMO-1", Scope: "demo", Path: "input/task/DEMO-1", Kind: "documentation", Content: content}}, Usage: map[string]int{}}
	if err = corpus.Validate(req, res); err != nil {
		t.Fatalf("normalized Python task source rejected: %v", err)
	}
	res.Sources[0].Content += " "
	if corpus.Validate(req, res) == nil {
		t.Fatal("modified source accepted")
	}
}
