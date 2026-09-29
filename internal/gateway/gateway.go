// Package gateway is the /v1 proxy coworkers' tools talk to.
//
// For each request it authenticates the caller, strips YardMaster's own credentials,
// labels the request for Switchyard (origin, per-person session, request ID), forwards it
// to switchyard-server on loopback, and records the gateway's side of the usage row.
package gateway

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
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"yardmaster/internal/auth"
	"yardmaster/internal/usage"
)

// Paths the gateway serves. Everything else under /v1 (decision, stats, stats/reset,
// session-stats) is admin-only and reached through YardMaster's own API.
var routedPaths = map[string]bool{
	"/v1/chat/completions":       true,
	"/v1/responses":              true,
	"/v1/messages":               true,
	"/v1/messages/count_tokens":  true,
	"/v1/responses/input_tokens": true,
	"/v1/responses/compact":      true,
}

const maxBody = 32 << 20 // coding agents send whole files

// Credentials are never forwarded to Switchyard: they belong to YardMaster, and would
// otherwise reach providers through forward_auth or fallback_client.
var credentialHeaders = []string{
	"Authorization", "X-Api-Key", "Cookie", "Proxy-Authorization", auth.ProxySecretHeader,
}

// Labels Switchyard records are set by the gateway alone, so callers can't claim to be
// someone else.
var labelHeaders = []string{
	"X-Switchyard-Origin", "X-Switchyard-Trial-Id", "X-Switchyard-Intake-Task",
}

// Session headers Switchyard reads, in its order of precedence (switchyard protocol
// crate, metadata.rs). The gateway prefixes the chosen value with the caller's name and
// sends it as x-switchyard-session-id, which takes precedence over the rest, so routing
// affinity never spans two people.
var sessionHeaders = []string{
	"X-Switchyard-Session-Id", "X-Claude-Code-Session-Id", "X-Nemo-Relay-Session-Id",
	"X-Session-Id", "Session-Id",
}

// Identify returns the caller for a request, or an error to send back.
type Identify func(r *http.Request) (*auth.Caller, error)

type Gateway struct {
	identify Identify
	ledger   *usage.Ledger
	proxy    *httputil.ReverseProxy
}

func New(switchyardURL string, identify Identify, ledger *usage.Ledger) *Gateway {
	target, _ := url.Parse(switchyardURL)
	g := &Gateway{identify: identify, ledger: ledger}
	g.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host
		},
		// Stream events as they arrive (also the default for text/event-stream).
		FlushInterval: -1,
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			ResponseHeaderTimeout: 0, // first byte can take minutes on long prompts
			MaxIdleConnsPerHost:   64,
			IdleConnTimeout:       90 * time.Second,
		},
		ModifyResponse: modifyResponse,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, context.Canceled) {
				return // the caller went away
			}
			slog.Warn("gateway could not reach switchyard-server", "err", err)
			writeError(w, r, http.StatusBadGateway, "router_unavailable",
				"The router is not reachable right now. Try again shortly.")
		},
	}
	return g
}

// ServeHTTP handles one /v1 request.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if r.URL.Path == "/v1/models" && r.Method == http.MethodGet {
		if _, err := g.identify(r); err != nil {
			writeError(w, r, http.StatusUnauthorized, "invalid_api_key", err.Error())
			return
		}
		g.forward(w, r)
		return
	}
	if !routedPaths[r.URL.Path] || r.Method != http.MethodPost {
		writeError(w, r, http.StatusNotFound, "not_found", "Unknown endpoint: "+r.Method+" "+r.URL.Path)
		return
	}
	caller, err := g.identify(r)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "invalid_api_key", err.Error())
		return
	}

	// Read the body once: to enforce the size limit and learn the route (model) name.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeError(w, r, http.StatusRequestEntityTooLarge, "request_too_large",
			fmt.Sprintf("Requests are limited to %d MB.", maxBody>>20))
		return
	}
	var peek struct {
		Model string `json:"model"`
	}
	json.Unmarshal(body, &peek)
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))

	requestID := newRequestID()
	label(r, caller.Username, requestID)

	rec := &recorder{ResponseWriter: w, status: http.StatusOK}
	g.forward(rec, r)

	e := usage.GatewayEvent{
		RequestID: requestID, At: start, UserID: caller.UserID, UserName: caller.Username,
		TokenID: caller.TokenID, TokenName: caller.TokenName, Route: peek.Model,
		Status: rec.status, LatencyMS: time.Since(start).Milliseconds(),
	}
	if r.Context().Err() != nil {
		e.Error = "the client disconnected"
		if rec.status == http.StatusOK {
			e.Status = 499
		}
	} else if rec.status >= 400 {
		e.Error = http.StatusText(rec.status)
	}
	g.ledger.AddGateway(e)
}

func (g *Gateway) forward(w http.ResponseWriter, r *http.Request) {
	for _, h := range credentialHeaders {
		r.Header.Del(h)
	}
	g.proxy.ServeHTTP(w, r)
}

// label sets the headers Switchyard records, replacing any the caller sent.
func label(r *http.Request, username, requestID string) {
	session := ""
	for _, h := range sessionHeaders {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			session = v
			break
		}
	}
	if session == "" {
		session = codexSession(r.Header.Get("X-Codex-Turn-Metadata"))
	}
	for _, h := range labelHeaders {
		r.Header.Del(h)
	}
	r.Header.Set("X-Switchyard-Origin", username)
	r.Header.Set("X-Switchyard-Trial-Id", requestID)
	if session != "" {
		r.Header.Set("X-Switchyard-Session-Id", username+":"+session)
	}
}

// codexSession reads the session ID Codex puts inside its turn-metadata header.
func codexSession(v string) string {
	if v == "" {
		return ""
	}
	var m struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal([]byte(v), &m)
	return m.SessionID
}

// modifyResponse hides provider and router internals on 5xx: the caller gets a
// generic 502 in the error shape its SDK expects. 4xx pass through unchanged, because
// the caller can act on them ("context too long").
func modifyResponse(resp *http.Response) error {
	if resp.StatusCode < 500 {
		return nil
	}
	detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	slog.Warn("upstream error", "status", resp.StatusCode, "path", resp.Request.URL.Path,
		"detail", strings.TrimSpace(string(detail)))
	msg := fmt.Sprintf("The model provider or router failed (status %d). Try again, or ask your admin to check the Health page.", resp.StatusCode)
	body := errorBody(resp.Request.URL.Path, "upstream_error", msg)
	resp.StatusCode = http.StatusBadGateway
	resp.Status = "502 Bad Gateway"
	resp.Header = http.Header{"Content-Type": {"application/json"}}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	return nil
}

// writeError sends an error in the shape the caller's SDK expects: Anthropic's on
// /v1/messages, OpenAI's everywhere else.
func writeError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(errorBody(r.URL.Path, code, msg))
}

func errorBody(path, code, msg string) []byte {
	var v any
	if strings.HasPrefix(path, "/v1/messages") {
		v = map[string]any{"type": "error", "error": map[string]string{"type": anthropicType(code), "message": msg}}
	} else {
		v = map[string]any{"error": map[string]any{"message": msg, "type": code, "code": code}}
	}
	b, _ := json.Marshal(v)
	return b
}

func anthropicType(code string) string {
	switch code {
	case "invalid_api_key":
		return "authentication_error"
	case "not_found":
		return "not_found_error"
	case "request_too_large":
		return "request_too_large"
	default:
		return "api_error"
	}
}

// Token extracts the gateway token: "Authorization: Bearer ym_…" (OpenAI SDKs) or
// "x-api-key: ym_…" (Anthropic SDKs, Claude Code).
func Token(r *http.Request) string {
	if v, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(v)
	}
	return strings.TrimSpace(r.Header.Get("X-Api-Key"))
}

func newRequestID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return "ym-" + hex.EncodeToString(b)
}

// recorder notes the status sent.
type recorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *recorder) WriteHeader(code int) {
	if !r.wrote {
		r.status, r.wrote = code, true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	r.wrote = true
	return r.ResponseWriter.Write(b)
}

func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
