// Package jev lets TypeSafe's Jev act as the judge of Switchyard's capability classifier.
//
// Switchyard's judge speaks chat formats only; Jev answers typed questions about a state
// (POST /v1/systemone). The adapter is an OpenAI Chat Completions endpoint on loopback
// that Switchyard reaches as an ordinary openai_chat provider. For each judge request it
// asks Jev two questions in one call, the probability that the efficient model succeeds
// (a Noul) and the capability rule that best describes the task (a Choice), and answers
// with the CapabilityClassifierDecision JSON Switchyard expects. The rule's boundary
// follows from the rule, in code.
//
// The TypeSafe key is the provider key Switchyard sends as the bearer token; the adapter
// passes it on and keeps nothing. Other judge modes (escalation, custom) are refused.
package jev

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// PathPrefix is where the adapter serves its OpenAI-compatible API. A provider whose base
// URL is BaseURL(port) is a Jev provider.
const PathPrefix = "/typesafe/v1"

// DefaultUpstream is TypeSafe's evaluation endpoint.
const DefaultUpstream = "https://api.typesafe.ai/v1/systemone"

// BaseURL is the base URL Switchyard's config uses for the adapter.
func BaseURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", port, PathPrefix)
}

// The judge's response_format names the verdict schema, which tells the judge modes apart.
const capabilitySchema = "CapabilityClassifierDecision"

// Appended by Switchyard after a windowed conversation (llm_class.rs); it's an instruction
// to the judge, not the task.
const trailingInstruction = "Route the conversation above. Output ONLY the routing JSON object, nothing else."

// Jev's context is 32k tokens for the state plus the longest question. Each message is
// kept to this many characters, head and tail, which stays well inside it for text and
// code. A request Jev still finds too long is retried once with half.
const maxMessageChars = 30000

const maxBody = 32 << 20

// Adapter is the http.Handler Switchyard calls.
type Adapter struct {
	upstream string
	client   *http.Client
}

// New returns an adapter that calls Jev at upstream (DefaultUpstream in production).
func New(upstream string) *Adapter {
	return &Adapter{upstream: upstream, client: &http.Client{Timeout: 30 * time.Second}}
}

func (a *Adapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == PathPrefix+"/chat/completions" && r.Method == http.MethodPost:
		a.handleChat(w, r)
	case r.URL.Path == PathPrefix+"/models" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": []any{
			map[string]any{"id": "jev-latest", "object": "model", "owned_by": "typesafe"},
		}})
	default:
		writeError(w, http.StatusNotFound, "not_found", "the Jev adapter serves only chat completions")
	}
}

// ---- the chat request Switchyard sends ----

type chatRequest struct {
	Model         string        `json:"model"`
	Messages      []chatMessage `json:"messages"`
	Stream        bool          `json:"stream"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
	ResponseFormat *struct {
		Type       string `json:"type"`
		JSONSchema *struct {
			Name string `json:"name"`
		} `json:"json_schema"`
	} `json:"response_format"`
}

type chatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// text is a message's text: a plain string, or the text parts of a content array.
func (m chatMessage) text() string {
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(m.Content, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// isCapabilityJudge reports whether req is the capability classifier's judge call. With
// the JSON Schema response format (Switchyard's default) the schema name says so; with
// json_object, the capability prompt does.
func isCapabilityJudge(req *chatRequest, system string) bool {
	if rf := req.ResponseFormat; rf != nil && rf.JSONSchema != nil {
		return rf.JSONSchema.Name == capabilitySchema
	}
	return strings.Contains(system, "p_solve") && strings.Contains(system, "capability card")
}

// ---- the capability card ----

type rule struct {
	ID, Boundary, Text string
}

var cardLine = regexp.MustCompile(`(?m)^- ((?:SUP|UNC|LIM)-\d+) \[(supported|uncertain|unsupported)\]: (.+)$`)

// defaultCard is Switchyard v0.3.0's packaged capability card
// (crates/libsy/src/prompts/capability-classifier/prompt.md, Apache-2.0), used when the
// prompt has no card YardMaster can read.
var defaultCard = []rule{
	{"SUP-1", "supported", "Route to the Efficient model when the task provides a complete output contract and a deterministic local validator that covers the material requirements."},
	{"SUP-2", "supported", "Route to the Efficient model when all required inputs are available, the target environment can be inspected, and correctness can be verified end-to-end without inaccessible external state."},
	{"SUP-3", "supported", "Route to the Efficient model when mathematical behavior, interfaces, shapes, data types, tolerances, and performance requirements are explicit and exercised by a representative harness."},
	{"SUP-4", "supported", "Route to the Efficient model when the required mechanism is identified, the relevant search space is bounded, and the success condition is executable. Do not infer this rule merely from the task's technical domain."},
	{"SUP-5", "supported", "Route to the Efficient model when reconstruction or behavioral reproduction is constrained by an executable reference, parser, format specification, or checker strong enough to distinguish correct from merely plausible output."},
	{"UNC-1", "uncertain", "Treat the route as uncertain when multiple reasonable interpretations of preprocessing, representation, indexing, naming, or output placement would produce different results and neither the instructions nor a validator resolve the choice."},
	{"UNC-2", "uncertain", "Treat the route as uncertain when success requires finding every relevant item across heterogeneous inputs or environment state, but the task does not define the search boundary or provide a completeness check."},
	{"LIM-1", "unsupported", "Prefer the Capable model when correctness depends primarily on extracting precise information from noisy visual, temporal, or rendered media and no machine-checkable extraction or replay mechanism is available."},
	{"LIM-2", "unsupported", "Prefer the Capable model when success depends on reproducing undocumented reference behavior, hidden intermediate state, or an unknown configuration, and small deviations fail despite satisfying the visible specification."},
}

// The Choice's no-match option: Switchyard's "none", with boundary "unmatched".
const noRule = "none"

// card reads the capability rules from the judge's system prompt, so an admin's own
// prompt (the classifier's prompt override) is honored.
func card(system string) []rule {
	var rules []rule
	for _, m := range cardLine.FindAllStringSubmatch(system, -1) {
		rules = append(rules, rule{m[1], m[2], strings.TrimSpace(m[3])})
	}
	if len(rules) == 0 {
		return defaultCard
	}
	return rules
}

// ---- the Jev request ----

type jevRequest struct {
	Model     string         `json:"model"`
	State     map[string]any `json:"state"`
	Questions map[string]any `json:"questions"`
}

type jevResponse struct {
	Model   string `json:"model"`
	Answers struct {
		Success struct {
			Noul *float64 `json:"noul"`
		} `json:"success"`
		Rule struct {
			Choice string `json:"choice"`
		} `json:"rule"`
	} `json:"answers"`
	Usage struct {
		InputTokens int `json:"input_tokens"`
	} `json:"usage"`
}

// state is what Jev judges: the opening task and, when there is one, the latest user
// follow-up, as Switchyard's judge sees them. Assistant turns and tool results are left
// out: Jev is at its best without irrelevant detail.
func state(messages []chatMessage, limit int) (map[string]any, bool) {
	var user []string
	for _, m := range messages {
		if m.Role != "user" {
			continue
		}
		if t := strings.TrimSpace(m.text()); t != "" && t != trailingInstruction {
			user = append(user, t)
		}
	}
	if len(user) == 0 {
		return nil, false
	}
	s := map[string]any{"opening_task": clip(user[0], limit)}
	if last := user[len(user)-1]; len(user) > 1 && last != user[0] {
		s["latest_user_message"] = clip(last, limit)
	}
	return s, true
}

// clip keeps the head and tail of long text, where tasks state what they want.
func clip(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	head, tail := limit*2/3, limit/3
	for head > 0 && !isRuneStart(s[head]) {
		head--
	}
	cut := len(s) - tail
	for cut < len(s) && !isRuneStart(s[cut]) {
		cut++
	}
	return s[:head] + "\n[… trimmed …]\n" + s[cut:]
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

func questions(rules []rule) map[string]any {
	options := map[string]any{}
	for _, r := range rules {
		options[r.ID] = r.Text
	}
	options[noRule] = "None of the rules above describes the hardest requirement of the task."
	return map[string]any{
		"success": map[string]any{
			"type": "noul",
			"instructions": map[string]any{
				"question": "Will the efficient model complete the whole task in `opening_task` (and `latest_user_message`, if present) correctly on one fresh run? " +
					"The efficient model is a smaller, cheaper model than the capable one.",
			},
			"criteria": map[string]any{
				"true":  "Success: the efficient model completes the whole task correctly, as a final verifier would judge it. Typical of routine requests: lookups, simple code, formatting, translation, short writing, bounded well-specified changes.",
				"false": "Failure: any other outcome. Typical of deep multi-step reasoning, proofs, system design, subtle debugging, concurrency, security analysis and large refactors, where a smaller model is likely to be wrong or shallow.",
			},
		},
		"rule": map[string]any{
			"type":         "choice",
			"instructions": "Which rule best describes the hardest material requirement for completing the task in `opening_task` (and `latest_user_message`, if present)?",
			"criteria":     options,
		},
	}
}

// ---- handling ----

func (a *Adapter) handleChat(w http.ResponseWriter, r *http.Request) {
	key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || strings.TrimSpace(key) == "" {
		writeError(w, http.StatusUnauthorized, "authentication_error", "no TypeSafe API key: set the provider's key in YardMaster's setup wizard")
		return
	}
	var req chatRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "the request body isn't valid JSON")
		return
	}
	var system strings.Builder
	for _, m := range req.Messages {
		if m.Role == "system" || m.Role == "developer" {
			system.WriteString(m.text())
			system.WriteString("\n")
		}
	}
	if !isCapabilityJudge(&req, system.String()) {
		writeError(w, http.StatusBadRequest, "invalid_request_error",
			"Jev can only be the judge of a classifier route in capability mode; it can't answer requests or judge other modes")
		return
	}
	rules := card(system.String())

	var out *jevResponse
	var err error
	for limit := maxMessageChars; ; limit /= 2 {
		st, ok := state(req.Messages, limit)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request_error", "the judge request has no user message to judge")
			return
		}
		out, err = a.ask(r.Context(), key, jevRequest{Model: modelOr(req.Model), State: st, Questions: questions(rules)})
		var ue *upstreamError
		if limit == maxMessageChars && errors.As(err, &ue) && ue.status == http.StatusBadRequest && strings.Contains(ue.body, "max_tokens_exceeded") {
			continue
		}
		break
	}
	if err != nil {
		a.writeUpstreamError(w, err)
		return
	}
	verdict, err := toVerdict(out, rules)
	if err != nil {
		slog.Warn("Jev answer unusable", "err", err)
		writeError(w, http.StatusBadGateway, "upstream_error", "Jev's answer was incomplete")
		return
	}
	content, _ := json.Marshal(verdict)
	// The model callers configured (e.g. jev-latest), so usage records match the target.
	writeCompletion(w, &req, modelOr(req.Model), string(content), out.Usage.InputTokens)
}

func modelOr(m string) string {
	if m == "" {
		return "jev-latest"
	}
	return m
}

type verdict struct {
	Crux               string  `json:"crux"`
	PrimaryRule        string  `json:"primary_rule"`
	CapabilityBoundary string  `json:"capability_boundary"`
	PSolve             float64 `json:"p_solve"`
}

func toVerdict(out *jevResponse, rules []rule) (verdict, error) {
	p := out.Answers.Success.Noul
	if p == nil || math.IsNaN(*p) {
		return verdict{}, errors.New("no success probability")
	}
	v := verdict{PrimaryRule: noRule, CapabilityBoundary: "unmatched", PSolve: math.Round(min(max(*p, 0), 1)*100) / 100}
	for _, r := range rules {
		if r.ID == out.Answers.Rule.Choice {
			v.PrimaryRule, v.CapabilityBoundary = r.ID, r.Boundary
		}
	}
	// Jev writes no rationale; the crux names what decided, for Switchyard's logs.
	v.Crux = fmt.Sprintf("Jev: rule %s, success probability %.2f", v.PrimaryRule, v.PSolve)
	return v, nil
}

type upstreamError struct {
	status     int
	body       string
	retryAfter string
}

func (e *upstreamError) Error() string { return fmt.Sprintf("TypeSafe answered %d", e.status) }

func (a *Adapter) ask(ctx context.Context, key string, jr jevRequest) (*jevResponse, error) {
	body, err := json.Marshal(jr)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.upstream, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		// The error body is TypeSafe's error code, never the state.
		return nil, &upstreamError{status: resp.StatusCode, body: string(data), retryAfter: resp.Header.Get("Retry-After")}
	}
	var out jevResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decoding Jev's answer: %w", err)
	}
	return &out, nil
}

// writeUpstreamError keeps the status classes Switchyard's retries depend on: 429 and 5xx
// are retried, other 4xx aren't.
func (a *Adapter) writeUpstreamError(w http.ResponseWriter, err error) {
	var ue *upstreamError
	switch {
	case errors.As(err, &ue):
		slog.Warn("TypeSafe refused a judge call", "status", ue.status, "body", truncate(ue.body, 200))
		switch {
		case ue.status == http.StatusUnauthorized || ue.status == http.StatusForbidden:
			writeError(w, ue.status, "authentication_error", "TypeSafe refused the API key")
		case ue.status == http.StatusTooManyRequests:
			if ue.retryAfter != "" {
				w.Header().Set("Retry-After", ue.retryAfter)
			}
			writeError(w, ue.status, "rate_limit_error", "TypeSafe's rate limit was reached")
		case ue.status >= 500:
			writeError(w, http.StatusBadGateway, "upstream_error", fmt.Sprintf("TypeSafe answered %d", ue.status))
		default:
			writeError(w, ue.status, "invalid_request_error", "TypeSafe refused the request: "+truncate(ue.body, 200))
		}
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
		writeError(w, http.StatusGatewayTimeout, "timeout", "TypeSafe didn't answer in time")
	default:
		slog.Warn("calling TypeSafe", "err", err)
		writeError(w, http.StatusBadGateway, "upstream_error", "couldn't reach TypeSafe")
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// ---- the chat response ----

func writeCompletion(w http.ResponseWriter, req *chatRequest, model, content string, inputTokens int) {
	id := "chatcmpl-jev-" + randomID()
	created := time.Now().Unix()
	usage := map[string]int{"prompt_tokens": inputTokens, "completion_tokens": 0, "total_tokens": inputTokens}
	if !req.Stream {
		writeJSON(w, http.StatusOK, map[string]any{
			"id": id, "object": "chat.completion", "created": created, "model": model,
			"choices": []any{map[string]any{
				"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": content},
			}},
			"usage": usage,
		})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	chunk := func(choices []any, extra map[string]any) {
		c := map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": choices}
		for k, v := range extra {
			c[k] = v
		}
		b, _ := json.Marshal(c)
		fmt.Fprintf(w, "data: %s\n\n", b)
	}
	chunk([]any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": content}, "finish_reason": nil}}, nil)
	chunk([]any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, nil)
	if req.StreamOptions != nil && req.StreamOptions.IncludeUsage {
		chunk([]any{}, map[string]any{"usage": usage})
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func randomID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError answers in OpenAI's error shape, which Switchyard's client reads.
func writeError(w http.ResponseWriter, status int, kind, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"type": kind, "message": msg}})
}
