package jira

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
)

type Profile struct {
	ID            string   `json:"id"`
	DisplayName   string   `json:"display_name"`
	AllowedScopes []string `json:"allowed_scopes"`
}
type Issue struct {
	TaskID             string   `json:"task_id"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	AllowedScopes      []string `json:"allowed_scopes"`
	Question           string   `json:"question"`
}
type Seed struct {
	Profiles []Profile `json:"profiles"`
	Issues   []Issue   `json:"issues"`
}

func Load(path string) (*Seed, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	var s Seed
	if e = json.Unmarshal(b, &s); e != nil {
		return nil, e
	}
	if len(s.Profiles) == 0 || len(s.Issues) == 0 {
		return nil, errors.New("empty seed")
	}
	return &s, nil
}
func (s *Seed) Profile(id string) (Profile, bool) {
	for _, p := range s.Profiles {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}
func (s *Seed) Issue(id string) (Issue, bool) {
	for _, v := range s.Issues {
		if v.TaskID == id {
			return v, true
		}
	}
	return Issue{}, false
}
func Scopes(issue Issue, profile Profile) []string {
	allowed := map[string]bool{}
	for _, s := range profile.AllowedScopes {
		allowed[s] = true
	}
	result := []string{}
	for _, s := range issue.AllowedScopes {
		if allowed[s] {
			result = append(result, s)
		}
	}
	return result
}

var keyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[1-9][0-9]*$`)

func ParseURL(base, raw string) (string, error) {
	b, e := url.Parse(base)
	if e != nil {
		return "", e
	}
	u, e := url.Parse(raw)
	if e != nil || u.User != nil || !strings.EqualFold(u.Scheme, b.Scheme) || !strings.EqualFold(u.Host, b.Host) || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return "", errors.New("invalid Jira URL")
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) != 3 || parts[1] != "browse" || !keyPattern.MatchString(parts[2]) {
		return "", errors.New("invalid Jira URL")
	}
	return parts[2], nil
}
func URL(base, key string) string {
	return fmt.Sprintf("%s/browse/%s", strings.TrimRight(base, "/"), key)
}
