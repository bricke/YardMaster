package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func f(v float64) *float64 { return &v }

func sample() *Deployment {
	return &Deployment{
		Clients: []Client{{Name: "or", Format: "openai_chat", BaseURL: "https://openrouter.ai/api/v1", Auth: AuthKey, KeyEnv: "OPENROUTER_API_KEY"}},
		Targets: []Target{{Name: "strong", ModelID: "big", Client: "or"}, {Name: "weak", ModelID: "small", Client: "or"}},
		Routes: []Route{
			{Name: "a", ID: "smart", Type: RouteAuto, CapableTarget: "strong", EfficientTarget: "weak"},
			{Name: "c", ID: "judged", Type: RouteClassifier, StrongTarget: "strong", WeakTarget: "weak", ClassifierTarget: "weak", BaseThreshold: f(0.6)},
			{Name: "r", ID: "ab", Type: RouteRandom, Targets: []string{"strong", "weak"}, Weights: []float64{1, 3}},
		},
	}
}

func TestTOMLRoundTrip(t *testing.T) {
	d := sample()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	text, err := d.TOML()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"schema_version = 1", `api_key_env = "OPENROUTER_API_KEY"`, `mode = "capability"`, "base_threshold = 0.6"} {
		if !strings.Contains(text, want) {
			t.Errorf("TOML lacks %q:\n%s", want, text)
		}
	}
	p, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.KeyEnvNames(); len(got) != 1 || got[0] != "OPENROUTER_API_KEY" {
		t.Errorf("key names: %v", got)
	}
	cases := []struct{ route, model, target, tier string }{
		{"smart", "big", "strong", TierStrong},
		{"smart", "small", "weak", TierWeak},
		{"judged", "small", "weak", TierWeak},
		{"ab", "small", "weak", ""},
		{"unknown", "big", "strong", ""},
		{"smart", "nope", "", ""},
	}
	for _, c := range cases {
		target, tier := p.Resolve(c.route, c.model)
		if target != c.target || tier != c.tier {
			t.Errorf("Resolve(%s, %s) = %s, %s; want %s, %s", c.route, c.model, target, tier, c.target, c.tier)
		}
	}
}

func TestValidateNamesProblems(t *testing.T) {
	d := sample()
	d.Routes[0].CapableTarget = "missing"
	d.Routes[2].Weights = []float64{0, 0}
	d.Clients[0].KeyEnv = "not a var"
	err := d.Validate()
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{`capable model "missing"`, "at least one weight", "key variable"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

func TestDiff(t *testing.T) {
	d := Diff("a\nb\nc\n", "a\nx\nc\n")
	var ops string
	for _, l := range d {
		ops += l.Op
	}
	if ops != " -+ " || !Changed(d) {
		t.Fatalf("diff ops %q", ops)
	}
	if Changed(Diff("same\n", "same\n")) {
		t.Fatal("identical texts reported as changed")
	}
}

func TestDottedNames(t *testing.T) {
	d := &Deployment{
		Clients: []Client{{Name: "ics.gateway", Format: "openai_chat", BaseURL: "https://x/v1", Auth: AuthNone}},
		Targets: []Target{{Name: "qwen3.8-27b", ModelID: "qwen3.8-27b", Client: "ics.gateway"}},
		Routes:  []Route{{Name: "qwen", ID: "qwen", Type: RoutePassthrough, Target: "qwen3.8-27b"}},
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	text, _ := d.TOML()
	if !strings.Contains(text, `[targets."qwen3.8-27b"]`) {
		t.Fatalf("dotted name not quoted:\n%s", text)
	}
	p, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	if target, _ := p.Resolve("qwen", "qwen3.8-27b"); target != "qwen3.8-27b" {
		t.Fatalf("resolve %q", target)
	}
}

func TestReasoningControls(t *testing.T) {
	off := false
	d := &Deployment{
		Clients: []Client{
			{Name: "openai", Format: "openai_responses", BaseURL: "https://api.openai.com/v1", Auth: AuthNone},
			{Name: "anthropic", Format: "anthropic_messages", BaseURL: "https://api.anthropic.com", Auth: AuthNone},
			{Name: "qwen", Format: "openai_chat", BaseURL: "https://x/v1", Auth: AuthNone},
		},
		Targets: []Target{
			{Name: "luna", ModelID: "gpt-6-luna", Client: "openai", ReasoningEffort: "low"},
			{Name: "haiku", ModelID: "claude-haiku-4-5", Client: "anthropic", ThinkingBudget: 2048},
			{Name: "sonnet", ModelID: "claude-sonnet-5", Client: "anthropic", AnthropicEffort: "medium"},
			{Name: "qwen", ModelID: "qwen3.8-27b", Client: "qwen", EnableThinking: &off},
		},
		Routes: []Route{{Name: "r", ID: "r", Type: RoutePassthrough, Target: "luna"}},
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	text, _ := d.TOML()
	for _, want := range []string{`reasoning_effort = "low"`, `budget_tokens = 2048`, `effort = "medium"`, `enable_thinking = false`} {
		if !strings.Contains(text, want) {
			t.Errorf("TOML lacks %q:\n%s", want, text)
		}
	}
	// Each control only fits the providers that understand it.
	d.Targets[1].ReasoningEffort = "high"
	d.Targets[3].ThinkingBudget = 500
	err := d.Validate()
	if err == nil || !strings.Contains(err.Error(), "for Claude use effort") || !strings.Contains(err.Error(), "for Anthropic providers") {
		t.Fatalf("mismatched controls accepted: %v", err)
	}
}

func TestHistoryOrder(t *testing.T) {
	a := &Applier{historyDir: t.TempDir()}
	// Names from before the fraction was zero-padded sort by number, not as text.
	for _, id := range []string{"1790000000-42", "1790000000-7", "1790000001-3"} {
		os.WriteFile(filepath.Join(a.historyDir, id+".toml"), []byte(id), 0o600)
	}
	h, err := a.History()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range h {
		ids = append(ids, e.ID)
	}
	if got := strings.Join(ids, " "); got != "1790000001-3 1790000000-42 1790000000-7" {
		t.Errorf("newest first: %s", got)
	}
	if h[0].AppliedAt != 1790000001 {
		t.Errorf("applied at %d", h[0].AppliedAt)
	}
}

func TestHistoryKeepsTheNewest(t *testing.T) {
	a := &Applier{historyDir: t.TempDir()}
	for i := range historyKeep {
		id := fmt.Sprintf("1790000000-%d", i*100)
		os.WriteFile(filepath.Join(a.historyDir, id+".toml"), []byte(id), 0o600)
	}
	// Applies within the same second, as when a config is applied and a key replaced.
	a.saveHistory("second newest")
	a.saveHistory("newest")
	h, _ := a.History()
	if len(h) != historyKeep {
		t.Fatalf("kept %d entries", len(h))
	}
	for i, want := range []string{"newest", "second newest"} {
		if got, _ := a.HistoryConfig(h[i].ID); got != want {
			t.Errorf("entry %d is %q, want %q", i, got, want)
		}
	}
	if h[len(h)-1].ID != "1790000000-200" {
		t.Errorf("oldest kept is %s; the two oldest should be gone", h[len(h)-1].ID)
	}
}
