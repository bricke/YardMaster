package jev

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// fakeJev records what the adapter sends and answers with reply(call number).
type fakeJev struct {
	mu    sync.Mutex
	calls []jevRequest
	auth  []string
	reply func(n int, w http.ResponseWriter)
}

func (f *fakeJev) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req jevRequest
	json.NewDecoder(r.Body).Decode(&req)
	f.mu.Lock()
	f.calls = append(f.calls, req)
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	n := len(f.calls)
	f.mu.Unlock()
	f.reply(n, w)
}

func answer(p float64, rule string) func(int, http.ResponseWriter) {
	return func(_ int, w http.ResponseWriter) {
		json.NewEncoder(w).Encode(map[string]any{
			"model": "jev-1.13.0",
			"answers": map[string]any{
				"success": map[string]any{"type": "noul", "noul": p},
				"rule":    map[string]any{"type": "choice", "choice": rule, "confidence": 0.8},
			},
			"usage": map[string]any{"input_tokens": 950, "output_tokens": 30},
		})
	}
}

const capabilityPrompt = `You are a task-level probability forecaster for a model router.
p_solve ... Efficient-agent capability card
- SUP-1 [supported]: Route to the Efficient model when the task has a validator.
- LIM-2 [unsupported]: Prefer the Capable model when success depends on hidden state.
`

func judgeBody(schema string, stream bool, user ...string) string {
	msgs := []map[string]any{{"role": "system", "content": capabilityPrompt}}
	for _, u := range user {
		msgs = append(msgs, map[string]any{"role": "user", "content": u})
	}
	rf := map[string]any{"type": "json_object"}
	if schema != "" {
		rf = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": schema, "strict": true}}
	}
	body := map[string]any{"model": "jev-latest", "messages": msgs, "max_tokens": 1024, "stream": stream, "response_format": rf}
	if stream {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	b, _ := json.Marshal(body)
	return string(b)
}

func setup(t *testing.T, reply func(int, http.ResponseWriter)) (*fakeJev, http.Handler) {
	t.Helper()
	f := &fakeJev{reply: reply}
	up := httptest.NewServer(f)
	t.Cleanup(up.Close)
	return f, New(up.URL)
}

func post(h http.Handler, body string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", PathPrefix+"/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer ts-key")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCapabilityVerdict(t *testing.T) {
	f, h := setup(t, answer(0.873, "LIM-2"))
	rec := post(h, judgeBody(capabilitySchema, false, "Design a CRDT editor.", "yes, go ahead", trailingInstruction))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
		} `json:"usage"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	var v verdict
	if err := json.Unmarshal([]byte(out.Choices[0].Message.Content), &v); err != nil {
		t.Fatalf("content isn't a verdict: %q", out.Choices[0].Message.Content)
	}
	if v.PSolve != 0.87 || v.PrimaryRule != "LIM-2" || v.CapabilityBoundary != "unsupported" || v.Crux == "" {
		t.Errorf("verdict %+v", v)
	}
	if out.Model != "jev-latest" || out.Usage.PromptTokens != 950 {
		t.Errorf("model %q, prompt tokens %d", out.Model, out.Usage.PromptTokens)
	}
	// The key is passed on; the state is the opening task and the latest user message,
	// without Switchyard's own trailing instruction.
	if f.auth[0] != "Bearer ts-key" {
		t.Errorf("upstream auth %q", f.auth[0])
	}
	st := f.calls[0].State
	if st["opening_task"] != "Design a CRDT editor." || st["latest_user_message"] != "yes, go ahead" {
		t.Errorf("state %v", st)
	}
	// The Choice offers the prompt's own rules, plus no match.
	criteria := f.calls[0].Questions["rule"].(map[string]any)["criteria"].(map[string]any)
	if len(criteria) != 3 || criteria["SUP-1"] == nil || criteria[noRule] == nil {
		t.Errorf("rule options %v", criteria)
	}
}

func TestUnknownRuleIsUnmatched(t *testing.T) {
	_, h := setup(t, answer(0.4, "none"))
	rec := post(h, judgeBody(capabilitySchema, false, "Write a haiku."))
	var out struct {
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	var v verdict
	json.Unmarshal([]byte(out.Choices[0].Message.Content), &v)
	if v.PrimaryRule != noRule || v.CapabilityBoundary != "unmatched" {
		t.Errorf("verdict %+v", v)
	}
}

func TestDefaultCardWhenPromptHasNone(t *testing.T) {
	if got := card("a custom prompt without rules"); len(got) != len(defaultCard) {
		t.Fatalf("got %d rules", len(got))
	}
	if got := card(capabilityPrompt); len(got) != 2 || got[1] != (rule{"LIM-2", "unsupported", "Prefer the Capable model when success depends on hidden state."}) {
		t.Fatalf("parsed %+v", got)
	}
}

func TestOnlyTheCapabilityJudge(t *testing.T) {
	f, h := setup(t, answer(0.9, "SUP-1"))
	if rec := post(h, judgeBody("EscalationDecision", false, "task")); rec.Code != http.StatusBadRequest {
		t.Errorf("escalation judge: %d", rec.Code)
	}
	plain := `{"model":"jev-latest","messages":[{"role":"user","content":"hello"}]}`
	if rec := post(h, plain); rec.Code != http.StatusBadRequest {
		t.Errorf("ordinary chat request: %d", rec.Code)
	}
	if len(f.calls) != 0 {
		t.Errorf("TypeSafe called %d times", len(f.calls))
	}
	// json_object mode sends no schema name; the capability prompt identifies the judge.
	if rec := post(h, judgeBody("", false, "task")); rec.Code != 200 {
		t.Errorf("json_object capability judge: %d %s", rec.Code, rec.Body)
	}
}

func TestNeedsAKey(t *testing.T) {
	_, h := setup(t, answer(0.9, "SUP-1"))
	if rec := post(h, judgeBody(capabilitySchema, false, "task"), "Authorization", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("no key: %d", rec.Code)
	}
}

func TestUpstreamErrorsKeepRetryClasses(t *testing.T) {
	for _, c := range []struct {
		upstream, want int
	}{{401, 401}, {429, 429}, {500, 502}, {503, 502}} {
		_, h := setup(t, func(_ int, w http.ResponseWriter) {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(c.upstream)
			io.WriteString(w, `{"detail":{"error_type":"x"}}`)
		})
		rec := post(h, judgeBody(capabilitySchema, false, "task"))
		if rec.Code != c.want {
			t.Errorf("upstream %d: got %d", c.upstream, rec.Code)
		}
		if c.upstream == 429 && rec.Header().Get("Retry-After") != "2" {
			t.Errorf("Retry-After not passed on")
		}
	}
}

func TestTooLongIsRetriedShorter(t *testing.T) {
	f, h := setup(t, func(n int, w http.ResponseWriter) {
		if n == 1 {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"detail":{"error_type":"max_tokens_exceeded"}}`)
			return
		}
		answer(0.2, "LIM-2")(n, w)
	})
	long := strings.Repeat("stack trace line\n", 5000)
	if rec := post(h, judgeBody(capabilitySchema, false, long)); rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	first, second := f.calls[0].State["opening_task"].(string), f.calls[1].State["opening_task"].(string)
	if len(first) > maxMessageChars+100 || len(second) > maxMessageChars/2+100 {
		t.Errorf("state not clipped: %d then %d chars", len(first), len(second))
	}
}

func TestStreaming(t *testing.T) {
	_, h := setup(t, answer(0.9, "SUP-1"))
	rec := post(h, judgeBody(capabilitySchema, true, "task"))
	body := rec.Body.String()
	if rec.Header().Get("Content-Type") != "text/event-stream" || !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("not an event stream: %q", body)
	}
	if !strings.Contains(body, `\"p_solve\":0.9`) || !strings.Contains(body, `"prompt_tokens":950`) || !strings.Contains(body, `"finish_reason":"stop"`) {
		t.Errorf("stream missing verdict, usage or finish: %s", body)
	}
}

func TestClipKeepsValidUTF8(t *testing.T) {
	s := strings.Repeat("é日本", 20000)
	got := clip(s, 1001)
	if !utf8.ValidString(got) || len(got) > 1100 || !strings.Contains(got, "trimmed") {
		t.Errorf("clip: %d bytes, valid %v", len(got), utf8.ValidString(got))
	}
	if clip("short", 100) != "short" {
		t.Error("short text changed")
	}
}
