package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"yardmaster/internal/auth"
	"yardmaster/internal/deploy"
	"yardmaster/internal/store"
	"yardmaster/internal/usage"
)

func newGateway(t *testing.T, upstream http.HandlerFunc) *Gateway {
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	db := store.OpenTest(t)
	prices, _ := usage.NewPrices(t.Context(), db)
	ledger := usage.NewLedger(db, prices, func() *deploy.Parsed { return nil })
	identify := func(r *http.Request) (*auth.Caller, error) {
		if Token(r) != "ym_good" {
			return nil, errors.New("bad token")
		}
		return &auth.Caller{UserID: 1, Username: "alice", TokenID: 2, TokenName: "laptop"}, nil
	}
	return New(srv.URL, identify, ledger)
}

func TestLabelsAndStripsCredentials(t *testing.T) {
	var got http.Header
	g := newGateway(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Write([]byte(`{"ok":true}`))
	})
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"smart"}`))
	req.Header.Set("X-Api-Key", "ym_good")
	req.Header.Set("Cookie", "ym_session=abc")
	req.Header.Set("X-Switchyard-Origin", "someone-else")
	req.Header.Set("X-Claude-Code-Session-Id", "s-42")
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	for _, h := range []string{"X-Api-Key", "Authorization", "Cookie"} {
		if got.Get(h) != "" {
			t.Errorf("%s was forwarded", h)
		}
	}
	if got.Get("X-Switchyard-Origin") != "alice" {
		t.Errorf("origin %q", got.Get("X-Switchyard-Origin"))
	}
	if got.Get("X-Switchyard-Session-Id") != "alice:s-42" {
		t.Errorf("session %q", got.Get("X-Switchyard-Session-Id"))
	}
	if !strings.HasPrefix(got.Get("X-Switchyard-Trial-Id"), "ym-") {
		t.Errorf("trial id %q", got.Get("X-Switchyard-Trial-Id"))
	}
}

func TestErrorsUseTheCallersShape(t *testing.T) {
	g := newGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal provider stack trace"))
	})
	for _, c := range []struct {
		path, token string
		status      int
		anthropic   bool
	}{
		{"/v1/chat/completions", "ym_bad", 401, false},
		{"/v1/messages", "", 401, true},
		{"/v1/chat/completions", "ym_good", 502, false},
		{"/v1/stats/reset", "ym_good", 404, false},
	} {
		req := httptest.NewRequest("POST", c.path, strings.NewReader(`{"model":"smart"}`))
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		body, _ := io.ReadAll(rec.Body)
		if rec.Code != c.status {
			t.Errorf("%s: status %d, want %d", c.path, rec.Code, c.status)
		}
		if strings.Contains(string(body), "stack trace") {
			t.Errorf("%s: upstream internals leaked: %s", c.path, body)
		}
		var v map[string]any
		json.Unmarshal(body, &v)
		if c.anthropic != (v["type"] == "error") || v["error"] == nil {
			t.Errorf("%s: wrong error shape %s", c.path, body)
		}
	}
}
