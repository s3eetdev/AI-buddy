// Package evidence checks the integrity and permissions of model-supplied evidence.
package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/s3eetdev/AI-buddy/backend/internal/integrations/ai"
)

type Corpus struct {
	sources map[string]ai.Source
	digest  string
}

func Load(path string) (*Corpus, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	var sources []ai.Source
	if e = json.Unmarshal(b, &sources); e != nil {
		return nil, e
	}
	if sources == nil {
		return nil, errors.New("invalid corpus")
	}
	digest := sha256.Sum256(b)
	c := &Corpus{sources: map[string]ai.Source{}, digest: hex.EncodeToString(digest[:])}
	for _, s := range sources {
		if s.ID == "" {
			return nil, errors.New("invalid corpus source")
		}
		if _, exists := c.sources[s.ID]; exists {
			return nil, errors.New("duplicate corpus source")
		}
		c.sources[s.ID] = s
	}
	return c, nil
}
func (c *Corpus) Digest() string { return c.digest }

// SplitLines reproduces Python str.splitlines(), including CRLF and Unicode separators.
func SplitLines(s string) []string {
	out := []string{}
	start := 0
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		sep := r == '\n' || r == '\r' || r == '\v' || r == '\f' || r == '\x1c' || r == '\x1d' || r == '\x1e' || r == '\x85' || r == '\u2028' || r == '\u2029'
		if sep {
			out = append(out, s[start:i])
			i += n
			if r == '\r' && i < len(s) && s[i] == '\n' {
				i++
			}
			start = i
		} else {
			i += n
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
func length(s string) int            { return utf8.RuneCountInString(s) }
func bounded(s string, max int) bool { return length(strings.TrimSpace(s)) > 0 && length(s) <= max }
func invalid() error                 { return errors.New("invalid_ai_evidence") }
func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// Validate compares sources to trusted snapshots, and verifies structural references and citations.
func (c *Corpus) Validate(req ai.Request, res ai.Response) error {
	if err := ai.ValidateInput(req); err != nil {
		return invalid()
	}
	req = ai.NormalizeRequest(req)
	if res.TaskID != req.TaskID || !contains([]string{"ready", "needs_clarification", "insufficient_context"}, res.Status) || !bounded(res.Plan.Summary, 4000) || len(res.Plan.Findings) > 20 || len(res.Plan.Steps) > 12 || len(res.Plan.Questions) > 12 || len(res.Plan.Limitations) > 12 || res.Plan.Findings == nil || res.Plan.Steps == nil || res.Plan.Questions == nil || res.Plan.Limitations == nil || res.Sources == nil || res.Usage == nil {
		return invalid()
	}
	if res.Status == "ready" && (len(res.Plan.Findings) == 0 || len(res.Plan.Steps) == 0 || len(res.Plan.Questions) > 0) {
		return invalid()
	}
	byID := make(map[string]ai.Source, len(res.Sources))
	for _, src := range res.Sources {
		if src.ID == "" || !contains(req.AllowedScopes, src.Scope) || !contains([]string{"code", "documentation", "contract", "test", "catalog"}, src.Kind) || length(src.Content) < 1 || length(src.Content) > 30000 {
			return invalid()
		}
		if _, ok := byID[src.ID]; ok {
			return invalid()
		}
		byID[src.ID] = src
		if src.ID == "task:"+req.TaskID {
			if len(req.AllowedScopes) == 0 || src.Scope != req.AllowedScopes[0] || src.Path != "input/task/"+req.TaskID || src.Kind != "documentation" || src.Content != ai.TaskSourceContent(req) {
				return invalid()
			}
		} else {
			trusted, ok := c.sources[src.ID]
			if !ok || trusted != src {
				return invalid()
			}
		}
	}
	for _, f := range res.Plan.Findings {
		if !contains([]string{"fact", "hypothesis"}, f.Kind) || !contains([]string{"entry_point", "change_candidate", "context", "relationship"}, f.Role) || !bounded(f.Explanation, 3000) || len(f.Citations) > 8 || (f.Kind == "fact" && len(f.Citations) == 0) {
			return invalid()
		}
		for _, citation := range f.Citations {
			src, ok := byID[citation.SourceID]
			if !ok || !bounded(citation.Quote, 3000) {
				return invalid()
			}
			lines := SplitLines(src.Content)
			if citation.StartLine < 1 || citation.EndLine < citation.StartLine || citation.EndLine > len(lines) {
				return invalid()
			}
			if !strings.Contains(strings.Join(lines[citation.StartLine-1:citation.EndLine], "\n"), citation.Quote) {
				return invalid()
			}
		}
	}
	for _, step := range res.Plan.Steps {
		if !bounded(step.Action, 2000) || !bounded(step.ExpectedResult, 2000) || len(step.SourceIDs) > 10 {
			return invalid()
		}
		for _, id := range step.SourceIDs {
			if _, ok := byID[id]; !ok {
				return invalid()
			}
		}
	}
	return nil
}
