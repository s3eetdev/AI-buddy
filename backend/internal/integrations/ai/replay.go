package ai

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// TaskSourceContent matches Navigator's virtual source byte for byte.
func TaskSourceContent(r Request) string {
	r = NormalizeRequest(r)
	lines := []string{"Задача: " + r.TaskID, r.Title, r.Description, "Критерии приёмки:"}
	lines = append(lines, r.AcceptanceCriteria...)
	lines = append(lines, "Уточнение пользователя:", r.Question)
	return strings.Join(lines, "\n")
}

type Replay struct {
	issues  map[string]Request
	records map[string]Response
}

func NewReplay(dir, seedPath string) (*Replay, error) {
	b, e := os.ReadFile(seedPath)
	if e != nil {
		return nil, e
	}
	var seed struct {
		Issues []Request `json:"issues"`
	}
	if e = json.Unmarshal(b, &seed); e != nil {
		return nil, e
	}
	r := &Replay{issues: map[string]Request{}, records: map[string]Response{}}
	for _, issue := range seed.Issues {
		if issue.TaskID == "" || strings.ContainsAny(issue.TaskID, `/\`) {
			return nil, &Error{Code: "invalid_seed"}
		}
		r.issues[issue.TaskID] = issue
		data, err := os.ReadFile(filepath.Join(dir, issue.TaskID+".json"))
		if err != nil {
			continue
		}
		var response Response
		if err = json.Unmarshal(data, &response); err != nil {
			return nil, &Error{Code: "invalid_replay"}
		}
		r.records[issue.TaskID] = response
	}
	return r, nil
}
func equalStrings(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// Check refuses to replay changed task context, clarification, or reduced scopes.
func (r *Replay) Check(req Request) error {
	if err := ValidateInput(req); err != nil {
		return err
	}
	base, ok := r.issues[req.TaskID]
	if !ok || req.Question != "" || req.Title != base.Title || req.Description != base.Description || !equalStrings(req.AcceptanceCriteria, base.AcceptanceCriteria) {
		return &Error{Code: "replay_input_not_recorded"}
	}
	if len(req.AllowedScopes) == 0 {
		return nil
	}
	if !equalStrings(req.AllowedScopes, base.AllowedScopes) {
		return &Error{Code: "replay_input_not_recorded"}
	}
	resp, ok := r.records[req.TaskID]
	if !ok || resp.TaskID != req.TaskID {
		return &Error{Code: "replay_input_not_recorded"}
	}
	found := false
	for _, src := range resp.Sources {
		if src.ID == "task:"+req.TaskID {
			found = true
			if src.Content != TaskSourceContent(req) {
				return &Error{Code: "replay_input_not_recorded"}
			}
		}
	}
	if !found && resp.Status != "insufficient_context" {
		return &Error{Code: "replay_input_not_recorded"}
	}
	return nil
}
func (r *Replay) Analyze(ctx context.Context, req Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, &Error{Code: "ai_cancelled", Retryable: true}
	}
	if err := r.Check(req); err != nil {
		return Response{}, err
	}
	if len(req.AllowedScopes) == 0 {
		p := Plan{Summary: "Недостаточно доступного контекста для анализа задачи.", Findings: []Finding{}, Steps: []Step{}, Questions: []string{"Уточните поведение и доступные источники задачи."}, Limitations: []string{"Отсутствие результатов поиска не доказывает отсутствие сервиса."}}
		return Response{TaskID: req.TaskID, Status: "insufficient_context", Plan: p, Sources: []Source{}, Usage: map[string]int{}, JiraMarkdown: "# План изучения задачи\n\n" + p.Summary + "\n\n## Следующие шаги\n\n## Вопросы наставнику\n\n- " + p.Questions[0] + "\n\n## Ограничения\n\n- " + p.Limitations[0]}, nil
	}
	return r.records[req.TaskID], nil
}
