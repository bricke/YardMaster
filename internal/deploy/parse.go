package deploy

import (
	"sort"

	"github.com/BurntSushi/toml"
)

// Parsed is what YardMaster needs to know about an applied config, whether it came from
// the wizard or was written by hand: which environment variables hold keys, and which
// target and tier each routed model belongs to.
type Parsed struct {
	Clients map[string]parsedClient `toml:"llm_clients"`
	Targets map[string]parsedTarget `toml:"targets"`
	Routes  map[string]parsedRoute  `toml:"routes"`
}

type parsedClient struct {
	Format    string `toml:"format"`
	BaseURL   string `toml:"base_url"`
	APIKeyEnv string `toml:"api_key_env"`
}

type parsedTarget struct {
	ID        string `toml:"id"`
	LLMClient string `toml:"llm_client"`
}

type parsedRoute struct {
	ID               string   `toml:"id"`
	Type             string   `toml:"type"`
	Mode             string   `toml:"mode"`
	Target           string   `toml:"target"`
	Targets          []string `toml:"targets"`
	CapableTarget    string   `toml:"capable_target"`
	EfficientTarget  string   `toml:"efficient_target"`
	StrongTarget     string   `toml:"strong_target"`
	WeakTarget       string   `toml:"weak_target"`
	ClassifierTarget string   `toml:"classifier_target"`
	ExecutorTarget   string   `toml:"executor_target"`
	AdvisorTarget    string   `toml:"advisor_target"`
	Stage            struct {
		CapableTarget   string `toml:"capable_target"`
		EfficientTarget string `toml:"efficient_target"`
	} `toml:"stage"`
	Classifier struct {
		Target string `toml:"target"`
	} `toml:"classifier"`
}

// Parse reads the parts of a deployment TOML that YardMaster uses. Unknown keys are
// ignored; Switchyard's --dry-run is the real validator.
func Parse(text string) (*Parsed, error) {
	var p Parsed
	if _, err := toml.Decode(text, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// KeyEnvNames lists the environment variables the config reads API keys from.
func (p *Parsed) KeyEnvNames() []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range p.Clients {
		if c.APIKeyEnv != "" && !seen[c.APIKeyEnv] {
			seen[c.APIKeyEnv] = true
			out = append(out, c.APIKeyEnv)
		}
	}
	sort.Strings(out)
	return out
}

// Tiers used in usage records. Switchyard's own vocabulary is "strong" and "weak"; routes
// with a single destination (passthrough, random, advisor) have no tier.
const (
	TierStrong = "strong"
	TierWeak   = "weak"
)

// Resolve finds the target a routed model belongs to, and its tier on that route.
// Switchyard's routing records name the served model ID but not the target or, for the
// served call, the tier, so both come from the config.
func (p *Parsed) Resolve(routeID, modelID string) (target, tier string) {
	if p == nil {
		return "", ""
	}
	match := func(name string) bool { return name != "" && p.Targets[name].ID == modelID }
	for _, r := range p.Routes {
		if r.ID != routeID {
			continue
		}
		strong := []string{r.CapableTarget, r.StrongTarget, r.Stage.CapableTarget}
		weak := []string{r.EfficientTarget, r.WeakTarget, r.Stage.EfficientTarget}
		for _, name := range strong {
			if match(name) {
				return name, TierStrong
			}
		}
		for _, name := range weak {
			if match(name) {
				return name, TierWeak
			}
		}
		others := append([]string{r.Target, r.ExecutorTarget, r.ClassifierTarget, r.AdvisorTarget, r.Classifier.Target}, r.Targets...)
		for _, name := range others {
			if match(name) {
				return name, ""
			}
		}
	}
	// The model isn't named by that route (or the route is unknown): any target with
	// that model ID, preferring a stable choice.
	var names []string
	for name, t := range p.Targets {
		if t.ID == modelID {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) > 0 {
		return names[0], ""
	}
	return "", ""
}

// TargetNames lists every target in the config, sorted.
func (p *Parsed) TargetNames() []string {
	var out []string
	for name := range p.Targets {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// TargetModel returns a target's upstream model ID.
func (p *Parsed) TargetModel(name string) string { return p.Targets[name].ID }

// RouteIDs lists the model names callers can use.
func (p *Parsed) RouteIDs() []string {
	var out []string
	for _, r := range p.Routes {
		out = append(out, r.ID)
	}
	sort.Strings(out)
	return out
}

// RouteStrategy is a route's public name and how it decides, for the Help page.
type RouteStrategy struct {
	ID       string `json:"id"`
	Strategy string `json:"strategy"`
}

// RouteStrategies lists routes with their strategy. The classifier's escalation and
// custom modes behave differently enough to be named on their own.
func (p *Parsed) RouteStrategies() []RouteStrategy {
	out := []RouteStrategy{}
	for _, r := range p.Routes {
		s := r.Type
		if r.Type == RouteClassifier && (r.Mode == "escalation" || r.Mode == "custom") {
			s = r.Mode
		}
		out = append(out, RouteStrategy{ID: r.ID, Strategy: s})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
