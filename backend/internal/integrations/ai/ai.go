package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// Request is the complete, flat contract accepted by the Python service.
type Request struct {
	TaskID             string   `json:"task_id"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	AllowedScopes      []string `json:"allowed_scopes"`
	Question           string   `json:"question"`
}
type Citation struct {
	SourceID  string `json:"source_id"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Quote     string `json:"quote"`
}
type Finding struct {
	Kind        string     `json:"kind"`
	Role        string     `json:"role"`
	Explanation string     `json:"explanation"`
	Citations   []Citation `json:"citations"`
}
type Step struct {
	Action         string   `json:"action"`
	SourceIDs      []string `json:"source_ids"`
	ExpectedResult string   `json:"expected_result"`
}
type Plan struct {
	Summary     string    `json:"summary"`
	Findings    []Finding `json:"findings"`
	Steps       []Step    `json:"steps"`
	Questions   []string  `json:"questions"`
	Limitations []string  `json:"limitations"`
}
type Source struct {
	ID      string `json:"id"`
	Scope   string `json:"scope"`
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Content string `json:"content"`
}
type Response struct {
	TaskID       string         `json:"task_id"`
	Status       string         `json:"status"`
	Plan         Plan           `json:"plan"`
	Sources      []Source       `json:"sources"`
	Model        *string        `json:"model"`
	Usage        map[string]int `json:"usage"`
	JiraMarkdown string         `json:"jira_markdown"`
}

type Client interface {
	Analyze(context.Context, Request) (Response, error)
}

// Error deliberately never exposes provider response bodies or credentials.
type Error struct {
	Code      string
	Retryable bool
}

func (e *Error) Error() string { return e.Code }

// NormalizeRequest mirrors Pydantic's str_strip_whitespace on the complete
// request, including each string in the two lists. It returns independent
// slices so callers can retain the original user answers in their history.
// Normalize only after enforcing limits on the unmodified accumulated text.
func NormalizeRequest(r Request) Request {
	r.TaskID = strings.TrimSpace(r.TaskID)
	r.Title = strings.TrimSpace(r.Title)
	r.Description = strings.TrimSpace(r.Description)
	r.Question = strings.TrimSpace(r.Question)
	if r.AcceptanceCriteria != nil {
		criteria := make([]string, len(r.AcceptanceCriteria))
		for i, v := range r.AcceptanceCriteria {
			criteria[i] = strings.TrimSpace(v)
		}
		r.AcceptanceCriteria = criteria
	}
	if r.AllowedScopes != nil {
		scopes := make([]string, len(r.AllowedScopes))
		for i, v := range r.AllowedScopes {
			scopes[i] = strings.TrimSpace(v)
		}
		r.AllowedScopes = scopes
	}
	return r
}

func ValidateInput(r Request) error {
	n := func(s string) int { return utf8.RuneCountInString(s) }
	if n(strings.TrimSpace(r.TaskID)) < 1 || n(r.TaskID) > 100 || n(strings.TrimSpace(r.Title)) < 1 || n(r.Title) > 500 || n(r.Description) > 16000 || n(r.Question) > 2000 || len(r.AcceptanceCriteria) > 30 || len(r.AllowedScopes) > 50 {
		return &Error{Code: "invalid_ai_request"}
	}
	total := n(r.Title) + n(r.Description) + n(r.Question)
	for _, v := range r.AcceptanceCriteria {
		total += n(v)
	}
	if total > 28000 {
		return &Error{Code: "invalid_ai_request"}
	}
	return nil
}

// Live calls the AI service. No response body is ever included in an error.
type Live struct {
	endpoint, token string
	http            *http.Client
}

func NewLive(baseURL, token string, timeout time.Duration) *Live {
	return &Live{endpoint: strings.TrimRight(baseURL, "/") + "/v1/analyze", token: token, http: &http.Client{Timeout: timeout}}
}
func (c *Live) Analyze(ctx context.Context, r Request) (Response, error) {
	var out Response
	if err := ValidateInput(r); err != nil {
		return out, err
	}
	r = NormalizeRequest(r)
	// Empty scopes must be [] rather than null: never imply full access.
	if r.AcceptanceCriteria == nil {
		r.AcceptanceCriteria = []string{}
	}
	if r.AllowedScopes == nil {
		r.AllowedScopes = []string{}
	}
	body, err := json.Marshal(r)
	if err != nil {
		return out, &Error{Code: "invalid_ai_request"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return out, &Error{Code: "ai_configuration_error"}
	}
	if u, e := url.Parse(c.endpoint); e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return out, &Error{Code: "ai_configuration_error"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	res, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return out, &Error{Code: "ai_cancelled", Retryable: true}
		}
		return out, &Error{Code: "ai_unavailable", Retryable: true}
	}
	defer res.Body.Close()
	const limit = 4 << 20
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return out, &Error{Code: "ai_unavailable", Retryable: true}
	}
	if len(b) > limit {
		return out, &Error{Code: "ai_response_too_large"}
	}
	if res.StatusCode != http.StatusOK {
		// Both ProviderError and FastAPI detail are accepted, but neither is echoed.
		var payload struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
			Detail json.RawMessage `json:"detail"`
		}
		_ = json.Unmarshal(b, &payload)
		if res.StatusCode == 401 || res.StatusCode == 403 {
			return out, &Error{Code: "ai_unauthorized"}
		}
		if res.StatusCode == 400 || res.StatusCode == 422 {
			return out, &Error{Code: "ai_request_rejected"}
		}
		if res.StatusCode >= 500 && (payload.Error.Code == "llm_not_configured" || payload.Error.Code == "llm_ungrounded_response" || payload.Error.Code == "llm_invalid_response" || payload.Error.Code == "service_token_not_configured" || string(payload.Detail) == `"service_token_not_configured"`) {
			return out, &Error{Code: "ai_invalid_response"}
		}
		if res.StatusCode == 502 || res.StatusCode == 503 || res.StatusCode == 504 || res.StatusCode >= 500 {
			return out, &Error{Code: "ai_unavailable", Retryable: true}
		}
		return out, &Error{Code: "ai_request_rejected"}
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&out); err != nil {
		return Response{}, &Error{Code: "ai_invalid_response"}
	}
	if err = dec.Decode(&struct{}{}); err != io.EOF {
		return Response{}, &Error{Code: "ai_invalid_response"}
	}
	if out.Usage == nil || out.Sources == nil || out.Plan.Findings == nil || out.Plan.Steps == nil || out.Plan.Questions == nil || out.Plan.Limitations == nil {
		return Response{}, &Error{Code: "ai_invalid_response"}
	}
	return out, nil
}
