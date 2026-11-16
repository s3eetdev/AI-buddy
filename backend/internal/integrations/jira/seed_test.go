package jira

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSeedAndScopeIntersection(t *testing.T) {
	s, err := Load("../../../../data/demo_tasks.json")
	if err != nil {
		t.Fatal(err)
	}
	i, ok := s.Issue("DEMO-5")
	if !ok {
		t.Fatal("missing issue")
	}
	p, ok := s.Profile("intern")
	if !ok {
		t.Fatal("missing profile")
	}
	if got := Scopes(i, p); !reflect.DeepEqual(got, []string{"demo"}) {
		t.Fatalf("scope escalation: %v", got)
	}
	if _, ok = s.Profile("missing"); ok {
		t.Fatal("invented profile")
	}
	if _, ok = s.Issue("DEMO-999"); ok {
		t.Fatal("invented issue")
	}
	n, _ := s.Profile("newcomer")
	if len(Scopes(i, n)) != 0 {
		t.Fatal("newcomer gained access")
	}
}

func TestRejectBadSeed(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing file accepted")
	}
	for _, content := range []string{"{", "{}", `{"profiles":[{"id":"intern"}],"issues":[]}`} {
		p := filepath.Join(t.TempDir(), "seed.json")
		if err := os.WriteFile(p, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(p); err == nil {
			t.Fatal("invalid seed accepted")
		}
	}
}

func TestJiraURLBoundary(t *testing.T) {
	base := "https://demo-jira.local"
	valid := URL(base+"/", "DEMO-1")
	if got, err := ParseURL(base, valid); err != nil || got != "DEMO-1" {
		t.Fatalf("%s %v", got, err)
	}
	for _, raw := range []string{
		"https://evil.local/browse/DEMO-1", "http://demo-jira.local/browse/DEMO-1",
		"https://user:pass@demo-jira.local/browse/DEMO-1", valid + "?token=1", valid + "#frag",
		base + "/browse/DEMO-0", base + "/browse/demo-1", base + "/browse/DEMO-1/extra",
		base + "/browse/%44EMO-1", "%", base + "/DEMO-1",
	} {
		if _, err := ParseURL(base, raw); err == nil {
			t.Errorf("unsafe URL accepted: %s", raw)
		}
	}
	if _, err := ParseURL("%", valid); err == nil {
		t.Fatal("invalid base accepted")
	}
}
