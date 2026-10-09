// Package deploy turns the setup wizard's model into Switchyard's deployment TOML, reads
// applied TOML back, and applies new configs safely.
package deploy

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"yardmaster/internal/jev"
	"yardmaster/internal/secrets"
)

// Client formats Switchyard supports.
var Formats = []string{"openai_chat", "openai_responses", "anthropic_messages"}

// Route types the wizard builds (brief §4). Other types can still be applied as raw TOML.
const (
	RouteAuto        = "auto"
	RouteClassifier  = "llm_classifier"
	RoutePassthrough = "passthrough"
	RouteRandom      = "random"
)

// Client auth modes. forward_auth is deliberately absent: the gateway strips callers'
// credentials, so there is nothing to forward.
const (
	AuthKey  = "key"
	AuthNone = "none"
)

// Names are TOML table keys. Dots are fine: the encoder quotes such keys, and Switchyard
// accepts them (model names like qwen3.8-27b are common).
var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

// Deployment is what the setup wizard edits.
type Deployment struct {
	Clients []Client `json:"clients"`
	Targets []Target `json:"targets"`
	Routes  []Route  `json:"routes"`
}

// Client is how to reach a provider ([llm_clients.<name>]).
type Client struct {
	Name       string `json:"name"`
	Format     string `json:"format"`
	BaseURL    string `json:"base_url"`
	Auth       string `json:"auth"`
	KeyEnv     string `json:"key_env,omitempty"`
	MaxRetries *int   `json:"max_retries,omitempty"`
	TimeoutMS  *int   `json:"timeout_ms,omitempty"`
}

// IsJev reports whether the client is YardMaster's Jev judge adapter (package jev).
func (c Client) IsJev() bool {
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return false
	}
	h := u.Hostname()
	return (h == "127.0.0.1" || h == "localhost") && strings.TrimSuffix(u.Path, "/") == jev.PathPrefix
}

// Target is one model on one client ([targets.<name>]).
type Target struct {
	Name         string `json:"name"`
	ModelID      string `json:"model_id"`
	Client       string `json:"client"`
	SystemPrompt string `json:"system_prompt,omitempty"`

	// Reasoning controls. Providers take different fields, so each applies only to
	// clients that understand it (see ReasoningControls below).

	// ReasoningEffort is forced on every request (Switchyard's target reasoning_effort):
	// reasoning_effort on openai_chat, reasoning.effort on openai_responses.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// AnthropicEffort sets output_config.effort, for Claude models with adaptive thinking
	// (4.6 and later). A default: the caller's own value wins.
	AnthropicEffort string `json:"anthropic_effort,omitempty"`
	// ThinkingBudget turns on extended thinking with that many tokens, for Claude models
	// that only have extended thinking (Haiku 4.5, Sonnet 4.5, Opus 4.5 and earlier).
	ThinkingBudget int `json:"thinking_budget,omitempty"`
	// EnableThinking sets chat_template_kwargs.enable_thinking, the switch vLLM-served
	// hybrid models such as Qwen use. nil leaves the model's default.
	EnableThinking *bool `json:"enable_thinking,omitempty"`
}

// Reasoning values each provider accepts, as of 2026-09 (OpenAI's reasoning guide,
// Anthropic's effort and extended-thinking docs). Which values a given model accepts
// varies; the provider rejects the ones it doesn't.
var (
	OpenAIEfforts    = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}
	AnthropicEfforts = []string{"low", "medium", "high", "xhigh", "max"}
)

const minThinkingBudget = 1024

// Route is what callers name as their model ([routes.<name>]).
type Route struct {
	Name          string `json:"name"`
	ID            string `json:"id"`
	Type          string `json:"type"`
	ContextWindow *int   `json:"context_window,omitempty"`

	// passthrough
	Target string `json:"target,omitempty"`
	// random
	Targets []string  `json:"targets,omitempty"`
	Weights []float64 `json:"weights,omitempty"`
	// auto
	CapableTarget   string `json:"capable_target,omitempty"`
	EfficientTarget string `json:"efficient_target,omitempty"`
	// llm_classifier, capability mode
	ClassifierTarget string   `json:"classifier_target,omitempty"`
	StrongTarget     string   `json:"strong_target,omitempty"`
	WeakTarget       string   `json:"weak_target,omitempty"`
	BaseThreshold    *float64 `json:"base_threshold,omitempty"`
	ThresholdStep    *float64 `json:"threshold_step,omitempty"`
	ClassifyTrigger  string   `json:"classify_trigger,omitempty"`
}

// Validate checks what YardMaster can check itself, with messages that name the field.
// Switchyard's --dry-run checks the rest.
func (d *Deployment) Validate() error {
	var errs []string
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	clients := map[string]bool{}
	formats := map[string]string{}
	jevClients := map[string]bool{}
	for _, c := range d.Clients {
		formats[c.Name] = c.Format
		if c.IsJev() {
			jevClients[c.Name] = true
			if c.Format != "openai_chat" {
				add("provider %q: TypeSafe's Jev is reached through YardMaster's adapter, which speaks openai_chat", c.Name)
			}
		}
		switch {
		case c.Name == "":
			add("a provider has no name: give it one, like openrouter")
		case !namePattern.MatchString(c.Name):
			add("provider name %q: use letters, digits, dots, - and _", c.Name)
		case clients[c.Name]:
			add("provider %q is defined twice", c.Name)
		}
		clients[c.Name] = true
		if !slices.Contains(Formats, c.Format) {
			add("provider %q: format must be one of %s", c.Name, strings.Join(Formats, ", "))
		}
		if !strings.HasPrefix(c.BaseURL, "http://") && !strings.HasPrefix(c.BaseURL, "https://") {
			add("provider %q: base URL must start with http:// or https://", c.Name)
		}
		switch c.Auth {
		case AuthKey:
			if !secrets.ValidName(c.KeyEnv) {
				add("provider %q: key variable must look like OPENROUTER_API_KEY, outside YARDMASTER_*", c.Name)
			}
		case AuthNone:
		default:
			add("provider %q: choose whether it needs an API key", c.Name)
		}
		if c.MaxRetries != nil && (*c.MaxRetries < 0 || *c.MaxRetries > 10) {
			add("provider %q: retries must be 0 to 10", c.Name)
		}
		if c.TimeoutMS != nil && *c.TimeoutMS < 1 {
			add("provider %q: timeout must be at least 1 ms", c.Name)
		}
	}

	targets := map[string]bool{}
	jevTargets := map[string]bool{}
	for _, t := range d.Targets {
		if jevClients[t.Client] {
			jevTargets[t.Name] = true
		}
		switch {
		case t.Name == "":
			add("a model has no name: give it a short label, like qwen")
		case !namePattern.MatchString(t.Name):
			add("model name %q: use letters, digits, dots, - and _ (this is only YardMaster's label; the provider's model ID goes in its own field)", t.Name)
		case targets[t.Name]:
			add("model %q is defined twice", t.Name)
		}
		targets[t.Name] = true
		if strings.TrimSpace(t.ModelID) == "" {
			add("model %q: the provider's model ID is required", t.Name)
		}
		format, ok := formats[t.Client]
		if !ok {
			add("model %q: provider %q doesn't exist", t.Name, t.Client)
			continue
		}
		anthropic := format == "anthropic_messages"
		if t.ReasoningEffort != "" {
			if anthropic {
				add("model %q: reasoning effort is for OpenAI-format providers; for Claude use effort or a thinking budget", t.Name)
			} else if !slices.Contains(OpenAIEfforts, t.ReasoningEffort) {
				add("model %q: reasoning effort must be one of %s", t.Name, strings.Join(OpenAIEfforts, ", "))
			}
		}
		if t.AnthropicEffort != "" {
			if !anthropic {
				add("model %q: effort is for Anthropic providers", t.Name)
			} else if !slices.Contains(AnthropicEfforts, t.AnthropicEffort) {
				add("model %q: effort must be one of %s", t.Name, strings.Join(AnthropicEfforts, ", "))
			}
		}
		if t.ThinkingBudget != 0 {
			if !anthropic {
				add("model %q: a thinking budget is for Anthropic providers", t.Name)
			} else if t.ThinkingBudget < minThinkingBudget {
				add("model %q: the thinking budget must be at least %d tokens", t.Name, minThinkingBudget)
			}
		}
		if t.EnableThinking != nil && anthropic {
			add("model %q: the thinking on/off switch is for vLLM-served models such as Qwen, not Claude", t.Name)
		}
	}

	ref := func(route, field, name string) {
		if name == "" {
			add("route %q: %s is required", route, field)
		} else if !targets[name] {
			add("route %q: %s %q isn't a defined model", route, field, name)
		} else if jevTargets[name] && field != "judge model" {
			add("route %q: %s %q is TypeSafe's Jev, which can only be the judge of a classifier route", route, field, name)
		}
	}
	routeNames, routeIDs := map[string]bool{}, map[string]bool{}
	for _, r := range d.Routes {
		switch {
		case !namePattern.MatchString(r.Name):
			add("route %q: internal name %q can use letters, digits, dots, - and _", r.ID, r.Name)
		case routeNames[r.Name]:
			add("route %q is defined twice", r.Name)
		}
		routeNames[r.Name] = true
		if strings.TrimSpace(r.ID) == "" {
			add("route %q: the model name callers use is required", r.Name)
		} else if routeIDs[r.ID] {
			add("route %q: model name %q is already used by another route", r.Name, r.ID)
		}
		routeIDs[r.ID] = true
		if r.ContextWindow != nil && *r.ContextWindow < 1 {
			add("route %q: context window must be positive", r.Name)
		}
		switch r.Type {
		case RoutePassthrough:
			ref(r.Name, "model", r.Target)
		case RouteRandom:
			if len(r.Targets) == 0 {
				add("route %q: pick at least one model", r.Name)
			}
			for _, t := range r.Targets {
				ref(r.Name, "model", t)
			}
			if len(r.Weights) > 0 {
				positive := false
				for _, w := range r.Weights {
					if w < 0 {
						add("route %q: weights can't be negative", r.Name)
					}
					positive = positive || w > 0
				}
				if len(r.Weights) != len(r.Targets) {
					add("route %q: give one weight per model", r.Name)
				} else if !positive {
					add("route %q: at least one weight must be above zero", r.Name)
				}
			}
		case RouteAuto:
			ref(r.Name, "capable model", r.CapableTarget)
			ref(r.Name, "efficient model", r.EfficientTarget)
		case RouteClassifier:
			ref(r.Name, "judge model", r.ClassifierTarget)
			ref(r.Name, "strong model", r.StrongTarget)
			ref(r.Name, "weak model", r.WeakTarget)
			if r.BaseThreshold == nil || *r.BaseThreshold < 0 || *r.BaseThreshold > 1 {
				add("route %q: threshold must be between 0 and 1", r.Name)
			} else if r.ThresholdStep != nil && (*r.ThresholdStep < 0 || *r.BaseThreshold+2**r.ThresholdStep > 1) {
				add("route %q: threshold step must be positive and threshold + 2 × step at most 1", r.Name)
			}
			if r.ClassifyTrigger != "" && !slices.Contains([]string{"every_request", "user_turn", "new_session"}, r.ClassifyTrigger) {
				add("route %q: unknown classify trigger %q", r.Name, r.ClassifyTrigger)
			}
		default:
			add("route %q: type must be auto, llm_classifier, passthrough or random", r.Name)
		}
	}
	if len(d.Routes) == 0 {
		add("add at least one route: it's what callers name as their model")
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "\n"))
	}
	return nil
}

// ---- TOML generation ----

type tomlFile struct {
	SchemaVersion int                   `toml:"schema_version"`
	LLMClients    map[string]tomlClient `toml:"llm_clients"`
	Targets       map[string]tomlTarget `toml:"targets"`
	Routes        map[string]tomlRoute  `toml:"routes"`
}

type tomlClient struct {
	Format     string `toml:"format"`
	BaseURL    string `toml:"base_url"`
	APIKeyEnv  string `toml:"api_key_env,omitempty"`
	MaxRetries *int   `toml:"max_retries,omitempty"`
	TimeoutMS  *int   `toml:"timeout_ms,omitempty"`
}

type tomlTarget struct {
	ID              string         `toml:"id"`
	LLMClient       string         `toml:"llm_client"`
	SystemPrompt    string         `toml:"system_prompt,omitempty"`
	ReasoningEffort string         `toml:"reasoning_effort,omitempty"`
	ExtraBody       map[string]any `toml:"extra_body,omitempty"`
}

type tomlRoute struct {
	ID               string    `toml:"id"`
	Type             string    `toml:"type"`
	ContextWindow    *int      `toml:"context_window,omitempty"`
	Target           string    `toml:"target,omitempty"`
	Targets          []string  `toml:"targets,omitempty"`
	Weights          []float64 `toml:"weights,omitempty"`
	CapableTarget    string    `toml:"capable_target,omitempty"`
	EfficientTarget  string    `toml:"efficient_target,omitempty"`
	Mode             string    `toml:"mode,omitempty"`
	ClassifierTarget string    `toml:"classifier_target,omitempty"`
	StrongTarget     string    `toml:"strong_target,omitempty"`
	WeakTarget       string    `toml:"weak_target,omitempty"`
	BaseThreshold    *float64  `toml:"base_threshold,omitempty"`
	ThresholdStep    *float64  `toml:"threshold_step,omitempty"`
	ClassifyTrigger  string    `toml:"classify_trigger,omitempty"`
}

// TOML renders the deployment as Switchyard's native TOML.
func (d *Deployment) TOML() (string, error) {
	f := tomlFile{
		SchemaVersion: 1,
		LLMClients:    map[string]tomlClient{},
		Targets:       map[string]tomlTarget{},
		Routes:        map[string]tomlRoute{},
	}
	for _, c := range d.Clients {
		tc := tomlClient{Format: c.Format, BaseURL: c.BaseURL, MaxRetries: c.MaxRetries, TimeoutMS: c.TimeoutMS}
		if c.Auth == AuthKey {
			tc.APIKeyEnv = c.KeyEnv
		}
		f.LLMClients[c.Name] = tc
	}
	for _, t := range d.Targets {
		tt := tomlTarget{ID: t.ModelID, LLMClient: t.Client, SystemPrompt: t.SystemPrompt, ReasoningEffort: t.ReasoningEffort}
		extra := map[string]any{}
		if t.AnthropicEffort != "" {
			extra["output_config"] = map[string]any{"effort": t.AnthropicEffort}
		}
		if t.ThinkingBudget > 0 {
			extra["thinking"] = map[string]any{"type": "enabled", "budget_tokens": t.ThinkingBudget}
		}
		if t.EnableThinking != nil {
			extra["chat_template_kwargs"] = map[string]any{"enable_thinking": *t.EnableThinking}
		}
		if len(extra) > 0 {
			tt.ExtraBody = extra
		}
		f.Targets[t.Name] = tt
	}
	for _, r := range d.Routes {
		tr := tomlRoute{ID: r.ID, Type: r.Type, ContextWindow: r.ContextWindow}
		switch r.Type {
		case RoutePassthrough:
			tr.Target = r.Target
		case RouteRandom:
			tr.Targets = r.Targets
			tr.Weights = r.Weights
		case RouteAuto:
			tr.CapableTarget, tr.EfficientTarget = r.CapableTarget, r.EfficientTarget
		case RouteClassifier:
			tr.Mode = "capability"
			tr.ClassifierTarget, tr.StrongTarget, tr.WeakTarget = r.ClassifierTarget, r.StrongTarget, r.WeakTarget
			tr.BaseThreshold, tr.ThresholdStep, tr.ClassifyTrigger = r.BaseThreshold, r.ThresholdStep, r.ClassifyTrigger
		}
		f.Routes[r.Name] = tr
	}
	var buf bytes.Buffer
	buf.WriteString("# Generated by YardMaster's setup wizard. Edits made here are replaced on the next apply.\n")
	enc := toml.NewEncoder(&buf)
	enc.Indent = ""
	if err := enc.Encode(f); err != nil {
		return "", err
	}
	return buf.String(), nil
}
