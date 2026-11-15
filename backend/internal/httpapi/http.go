package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/s3eetdev/AI-buddy/backend/internal/config"
	"github.com/s3eetdev/AI-buddy/backend/internal/evidence"
	"github.com/s3eetdev/AI-buddy/backend/internal/integrations/ai"
	"github.com/s3eetdev/AI-buddy/backend/internal/integrations/jira"
	"github.com/s3eetdev/AI-buddy/backend/internal/storage/postgres"
)

type Server struct {
	Config config.Config
	Store  *postgres.Store
	Seed   *jira.Seed
	Corpus *evidence.Corpus
	Replay *ai.Replay
}
type contextKey struct{}

var errAccess = errors.New("source access denied")
var errCorpus = errors.New("corpus mismatch")

func visibilityError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errAccess):
		fail(w, r, 403, "source_access_denied", "Нет доступа к сохранённым источникам")
	case errors.Is(err, errCorpus):
		fail(w, r, 409, "corpus_mismatch", "Сохранённая версия корпуса отличается от текущей")
	default:
		fail(w, r, 500, "invalid_stored_result", "Сохранённый результат не прошёл проверку")
	}
}

func ID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	s := hex.EncodeToString(b)
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { write(w, 200, map[string]any{"status": "ok"}) })
	m.HandleFunc("GET /ready", s.ready)
	m.HandleFunc("GET /api/v1/me", s.me)
	m.HandleFunc("GET /api/v1/demo/issues", s.issues)
	m.HandleFunc("POST /api/v1/analyses", s.create)
	m.HandleFunc("GET /api/v1/analyses", s.list)
	m.HandleFunc("GET /api/v1/analyses/{id}", s.get)
	m.HandleFunc("POST /api/v1/analyses/{id}/answers", s.answer)
	m.HandleFunc("PATCH /api/v1/analyses/{id}/steps/{step_id}", s.step)
	m.HandleFunc("GET /api/v1/analyses/{id}/sources/{source_id}", s.source)
	m.HandleFunc("POST /api/v1/analyses/{id}/publish", s.publish)
	m.HandleFunc("GET /api/v1/mock-jira/plans/{id}", s.plan)
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fail(w, r, http.StatusNotFound, "not_found", "Маршрут не найден")
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := ID()
		w.Header().Set("X-Request-ID", id)
		r = r.WithContext(context.WithValue(r.Context(), contextKey{}, id))
		origin := r.Header.Get("Origin")
		for _, v := range s.Config.CORSOrigins {
			if origin == v {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
				break
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		m.ServeHTTP(w, r)
	})
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	write(w, status, map[string]any{"error": map[string]any{"code": code, "message": msg, "request_id": r.Context().Value(contextKey{})}})
}
func storeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		fail(w, r, 404, "not_found", "Запись не найдена")
	case errors.Is(err, postgres.ErrConflict):
		fail(w, r, 409, "conflict", "Конфликт состояния или ключа операции")
	case errors.Is(err, postgres.ErrPublishFailed):
		fail(w, r, 500, "publish_failed", "Не удалось сохранить план. Повторите публикацию.")
	default:
		fail(w, r, 500, "internal_error", "Не удалось выполнить операцию")
	}
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		fail(w, r, 400, "invalid_json", "Некорректный JSON или поля запроса")
		return false
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		fail(w, r, 400, "invalid_json", "Некорректный JSON")
		return false
	}
	return true
}
func key(w http.ResponseWriter, r *http.Request) string {
	k := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(k) < 1 || len(k) > 200 {
		fail(w, r, 400, "invalid_idempotency_key", "Укажите Idempotency-Key длиной до 200 символов")
		return ""
	}
	return k
}
func hash(v any) string {
	b, _ := json.Marshal(v)
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:])
}
func (s *Server) profile() jira.Profile { p, _ := s.Seed.Profile(s.Config.Profile); return p }
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	_, ok := s.Seed.Profile(s.Config.Profile)
	if !ok || s.Store.Pool.Ping(r.Context()) != nil {
		fail(w, r, 503, "not_ready", "Сервис не готов")
		return
	}
	if err := s.Store.Ready(r.Context()); err != nil {
		fail(w, r, 503, "not_ready", "Схема базы недоступна")
		return
	}
	write(w, 200, map[string]string{"status": "ready"})
}
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]any{"auth_mode": "demo", "profile": s.profile()})
}
func (s *Server) issues(w http.ResponseWriter, r *http.Request) {
	items := []map[string]string{}
	for _, i := range s.Seed.Issues {
		items = append(items, map[string]string{"task_id": i.TaskID, "title": i.Title, "jira_url": jira.URL(s.Config.JiraBaseURL, i.TaskID)})
	}
	write(w, 200, map[string]any{"issues": items})
}
func request(i jira.Issue, p jira.Profile) ai.Request {
	return ai.Request{TaskID: i.TaskID, Title: i.Title, Description: i.Description, AcceptanceCriteria: i.AcceptanceCriteria, AllowedScopes: jira.Scopes(i, p), Question: i.Question}
}
func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		JiraURL string `json:"jira_url"`
	}
	if !decode(w, r, &body) {
		return
	}
	k := key(w, r)
	if k == "" {
		return
	}
	if prior, err := s.Store.IdempotentAnalysis(r.Context(), s.Config.Profile, "create", k, hash(body), ""); err != nil {
		storeError(w, r, err)
		return
	} else if prior != nil {
		if err := s.visible(prior); err != nil {
			visibilityError(w, r, err)
			return
		}
		write(w, 202, map[string]any{"analysis_id": prior.ID, "status": prior.Status, "status_url": "/api/v1/analyses/" + prior.ID})
		return
	}
	task, err := jira.ParseURL(s.Config.JiraBaseURL, body.JiraURL)
	if err != nil {
		fail(w, r, 422, "invalid_jira_url", "Некорректная ссылка Jira")
		return
	}
	issue, ok := s.Seed.Issue(task)
	if !ok {
		fail(w, r, 404, "jira_task_not_found", "Задача не найдена")
		return
	}
	req := request(issue, s.profile())
	if err = ai.ValidateInput(req); err != nil {
		fail(w, r, 422, "invalid_input", "Задача превышает ограничения")
		return
	}
	req = ai.NormalizeRequest(req)
	if s.Replay != nil {
		if err = s.Replay.Check(req); err != nil {
			fail(w, r, 422, "replay_input_not_recorded", "Для этих данных нет записи replay")
			return
		}
	}
	snapshot, _ := json.Marshal(issue)
	input, _ := json.Marshal(req)
	a, err := s.Store.CreateAnalysis(r.Context(), postgres.CreateInput{ID: ID(), JobID: ID(), Owner: s.Config.Profile, JiraURL: body.JiraURL, TaskID: task, IdempotencyKey: k, PayloadHash: hash(body), ExecutionMode: s.Config.AIMode, TaskSnapshot: snapshot, AIRequest: input})
	if err != nil {
		storeError(w, r, err)
		return
	}
	write(w, 202, map[string]any{"analysis_id": a.ID, "status": a.Status, "status_url": "/api/v1/analyses/" + a.ID})
}
func parseRevision(r *http.Request) (*int, error) {
	s := r.URL.Query().Get("result_revision")
	if s == "" {
		return nil, nil
	}
	n, e := strconv.Atoi(s)
	if e != nil || n < 1 {
		return nil, errors.New("invalid")
	}
	return &n, nil
}
func (s *Server) visible(a *postgres.Analysis) error {
	if a.ResultRevision == nil || len(a.Result) == 0 || string(a.Result) == "null" {
		return nil
	}
	var req ai.Request
	var res ai.Response
	if json.Unmarshal(a.AIRequest, &req) != nil || json.Unmarshal(a.Result, &res) != nil {
		return errors.New("invalid stored result")
	}
	issue, ok := s.Seed.Issue(a.TaskID)
	if !ok {
		return errAccess
	}
	req.AllowedScopes = jira.Scopes(issue, s.profile())
	for _, source := range res.Sources {
		allowed := false
		for _, scope := range req.AllowedScopes {
			if source.Scope == scope {
				allowed = true
				break
			}
		}
		if !allowed {
			return errAccess
		}
	}
	if a.CorpusDigest != s.Corpus.Digest() {
		return errCorpus
	}
	return s.Corpus.Validate(req, res)
}
func (s *Server) fetch(w http.ResponseWriter, r *http.Request, revision *int) *postgres.Analysis {
	a, err := s.Store.GetAnalysis(r.Context(), s.Config.Profile, r.PathValue("id"), revision)
	if err != nil {
		storeError(w, r, err)
		return nil
	}
	if err = s.visible(a); err != nil {
		visibilityError(w, r, err)
		return nil
	}
	return a
}
func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	rev, err := parseRevision(r)
	if err != nil {
		fail(w, r, 400, "invalid_revision", "Некорректная версия")
		return
	}
	a := s.fetch(w, r, rev)
	if a != nil {
		write(w, 200, a)
	}
}
func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	limit, offset := 20, 0
	var err error
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, err = strconv.Atoi(v)
		if err != nil || limit < 1 || limit > 100 {
			fail(w, r, 400, "invalid_pagination", "Некорректный limit")
			return
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		offset, err = strconv.Atoi(v)
		if err != nil || offset < 0 {
			fail(w, r, 400, "invalid_pagination", "Некорректный offset")
			return
		}
	}
	items, err := s.Store.ListAnalyses(r.Context(), s.Config.Profile, limit, offset)
	if err != nil {
		storeError(w, r, err)
		return
	}
	for i := range items {
		if err := s.visible(&items[i]); err != nil {
			visibilityError(w, r, err)
			return
		}
	}
	write(w, 200, map[string]any{"analyses": items, "limit": limit, "offset": offset})
}
func (s *Server) answer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Answer string `json:"answer"`
	}
	if !decode(w, r, &body) {
		return
	}
	k := key(w, r)
	if k == "" {
		return
	}
	if prior, err := s.Store.IdempotentAnalysis(r.Context(), s.Config.Profile, "answer", k, hash(body), r.PathValue("id")); err != nil {
		storeError(w, r, err)
		return
	} else if prior != nil {
		if err := s.visible(prior); err != nil {
			visibilityError(w, r, err)
			return
		}
		write(w, 202, map[string]any{"analysis_id": prior.ID, "status": prior.Status, "status_url": "/api/v1/analyses/" + prior.ID})
		return
	}
	if strings.TrimSpace(body.Answer) == "" {
		fail(w, r, 422, "invalid_answer", "Ответ не должен быть пустым")
		return
	}
	a := s.fetch(w, r, nil)
	if a == nil {
		return
	}
	if a.ResultRevision == nil {
		fail(w, r, 409, "conflict", "Нет версии для уточнения")
		return
	}
	var req ai.Request
	if json.Unmarshal(a.AIRequest, &req) != nil {
		fail(w, r, 409, "conflict", "Нет исходного запроса")
		return
	}
	var snapshot jira.Issue
	if json.Unmarshal(a.TaskSnapshot, &snapshot) != nil {
		fail(w, r, 500, "invalid_stored_result", "Не удалось прочитать историю задачи")
		return
	}
	answers, err := s.Store.Answers(r.Context(), s.Config.Profile, a.ID)
	if err != nil {
		storeError(w, r, err)
		return
	}
	req.Question = snapshot.Question
	for _, answer := range append(answers, body.Answer) {
		if req.Question != "" {
			req.Question += "\n"
		}
		req.Question += answer
	}
	if ai.ValidateInput(req) != nil {
		fail(w, r, 422, "question_too_long", "История ответов превышает допустимую длину")
		return
	}
	req = ai.NormalizeRequest(req)
	if s.Replay != nil {
		if err := s.Replay.Check(req); err != nil {
			fail(w, r, 422, "replay_input_not_recorded", "Для уточнения нет записи replay")
			return
		}
	}
	input, _ := json.Marshal(req)
	out, err := s.Store.SubmitAnswer(r.Context(), postgres.AnswerInput{AnalysisID: a.ID, JobID: ID(), Owner: s.Config.Profile, IdempotencyKey: k, PayloadHash: hash(body), Answer: body.Answer, ExpectedRevision: *a.ResultRevision, AIRequest: input})
	if err != nil {
		storeError(w, r, err)
		return
	}
	write(w, 202, map[string]any{"analysis_id": out.ID, "status": out.Status, "status_url": "/api/v1/analyses/" + out.ID})
}
func (s *Server) step(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Revision  int   `json:"result_revision"`
		Completed *bool `json:"completed"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Revision < 1 || body.Completed == nil {
		fail(w, r, 422, "invalid_input", "Укажите версию и completed")
		return
	}
	a := s.fetch(w, r, nil)
	if a == nil {
		return
	}
	if a.ResultRevision == nil || *a.ResultRevision != body.Revision {
		fail(w, r, 409, "conflict", "Версия изменилась")
		return
	}
	var result ai.Response
	_ = json.Unmarshal(a.Result, &result)
	found := false
	for n := range result.Plan.Steps {
		if r.PathValue("step_id") == fmt.Sprintf("v%d-s%d", body.Revision, n+1) {
			found = true
			break
		}
	}
	if !found {
		fail(w, r, 404, "not_found", "Шаг не найден")
		return
	}
	progress, err := s.Store.SetProgress(r.Context(), s.Config.Profile, a.ID, body.Revision, r.PathValue("step_id"), *body.Completed)
	if err != nil {
		storeError(w, r, err)
		return
	}
	write(w, 200, progress)
}
func (s *Server) source(w http.ResponseWriter, r *http.Request) {
	rev, err := parseRevision(r)
	if err != nil {
		fail(w, r, 400, "invalid_revision", "Некорректная версия")
		return
	}
	a := s.fetch(w, r, rev)
	if a == nil {
		return
	}
	var res ai.Response
	_ = json.Unmarshal(a.Result, &res)
	for _, src := range res.Sources {
		if src.ID == r.PathValue("source_id") {
			write(w, 200, src)
			return
		}
	}
	fail(w, r, 404, "not_found", "Источник не найден")
}
func (s *Server) publish(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Revision  int   `json:"result_revision"`
		Confirmed *bool `json:"confirmed"`
	}
	if !decode(w, r, &body) {
		return
	}
	k := key(w, r)
	if k == "" {
		return
	}
	if body.Revision < 1 || body.Confirmed == nil || !*body.Confirmed {
		fail(w, r, 422, "confirmation_required", "Требуется явное подтверждение и версия")
		return
	}
	a := s.fetch(w, r, nil)
	if a == nil {
		return
	}
	if a.ResultRevision == nil || *a.ResultRevision != body.Revision || a.Status != "ready" {
		fail(w, r, 409, "conflict", "Публикация недоступна для этой версии")
		return
	}
	var res ai.Response
	if json.Unmarshal(a.Result, &res) != nil || len(res.Plan.Findings) == 0 || len(res.Plan.Steps) == 0 || len(res.Sources) == 0 {
		fail(w, r, 409, "conflict", "Результат не готов к публикации")
		return
	}
	plan, err := s.Store.Publish(r.Context(), postgres.PublishInput{AnalysisID: a.ID, Owner: s.Config.Profile, IdempotencyKey: k, PayloadHash: hash(struct {
		ID       string
		Revision int
	}{a.ID, body.Revision}), Markdown: res.JiraMarkdown, Revision: body.Revision, Snapshot: a.Result})
	if err != nil {
		storeError(w, r, err)
		return
	}
	write(w, 200, map[string]any{"plan_id": plan.ID, "url": "/api/v1/mock-jira/plans/" + plan.ID})
}
func (s *Server) plan(w http.ResponseWriter, r *http.Request) {
	p, err := s.Store.GetPlan(r.Context(), s.Config.Profile, r.PathValue("id"))
	if err != nil {
		storeError(w, r, err)
		return
	}
	rev := p.Revision
	a, err := s.Store.GetAnalysis(r.Context(), s.Config.Profile, p.AnalysisID, &rev)
	if err != nil {
		storeError(w, r, err)
		return
	}
	if err := s.visible(a); err != nil {
		visibilityError(w, r, err)
		return
	}
	write(w, 200, p)
}
