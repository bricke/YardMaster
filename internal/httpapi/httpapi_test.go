package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yardmaster/internal/audit"
	"yardmaster/internal/auth"
	"yardmaster/internal/deploy"
	"yardmaster/internal/metrics"
	"yardmaster/internal/secrets"
	"yardmaster/internal/settings"
	"yardmaster/internal/store"
	"yardmaster/internal/supervisor"
	"yardmaster/internal/tlsca"
	"yardmaster/internal/usage"
)

func newServer(t *testing.T, cfg settings.Settings) (*Server, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	cfg.DataDir = dir
	if cfg.Auth == "" {
		cfg.Auth = settings.AuthBuiltin
	}
	db := store.OpenTest(t)
	keys, _ := secrets.Open(filepath.Join(dir, "secrets"), "")
	sup := supervisor.New(supervisor.Options{Bin: "false", ConfigPath: filepath.Join(dir, "config.toml"), Port: 1, ShutdownTimeout: time.Second,
		Env: func(string) ([]string, error) { return nil, nil }})
	applier, _ := deploy.NewApplier(db, sup, keys, filepath.Join(dir, "config.toml"), filepath.Join(dir, "history"))
	prices, _ := usage.NewPrices(t.Context(), db)
	certs, _ := tlsca.Open(filepath.Join(dir, "tls"))
	s := New(Deps{Settings: cfg, DB: db, Auth: auth.New(db), Audit: audit.New(db), Keys: keys, Sup: sup,
		Applier: applier, Ledger: usage.NewLedger(db, prices, applier.Parsed), Prices: prices,
		Metrics: metrics.NewScraper(sup.URL()), TLS: certs})
	return s, s.Handler()
}

func do(h http.Handler, method, path, body, cookie string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://ym.test"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if method != "GET" {
		req.Header.Set("Origin", "http://ym.test")
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRolesAreEnforcedByTheAPI(t *testing.T) {
	s, h := newServer(t, settings.Settings{})
	ctx := t.Context()
	admin, _ := s.Auth.CreateAdmin(ctx, "admin", "correct horse battery")
	adminCookie, _ := s.Auth.NewSession(ctx, admin.ID)
	user, temp, _ := s.Auth.CreateUser(ctx, "alice", "Alice")
	userCookie, _ := s.Auth.NewSession(ctx, user.ID)

	// Holding a temporary password, only changing it is allowed.
	if rec := do(h, "GET", "/api/me/tokens", "", userCookie); rec.Code != http.StatusForbidden {
		t.Fatalf("temporary password: status %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/password", `{"current":"`+temp+`","new":"alice's own password"}`, userCookie); rec.Code != 200 {
		t.Fatalf("change password: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/me/tokens", "", userCookie); rec.Code != 200 {
		t.Fatalf("after change: %d", rec.Code)
	}
	for _, path := range []string{"/api/admin/users", "/api/admin/usage", "/api/admin/audit", "/api/admin/keys"} {
		if rec := do(h, "GET", path, "", userCookie); rec.Code != http.StatusForbidden {
			t.Errorf("user reached %s: %d", path, rec.Code)
		}
		if rec := do(h, "GET", path, "", adminCookie); rec.Code != 200 {
			t.Errorf("admin couldn't reach %s: %d", path, rec.Code)
		}
	}
	if rec := do(h, "GET", "/api/admin/users", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", rec.Code)
	}
}

func TestDeletedUsersUsageIsntInherited(t *testing.T) {
	s, h := newServer(t, settings.Settings{})
	ctx := t.Context()
	admin, _ := s.Auth.CreateAdmin(ctx, "admin", "correct horse battery")
	adminCookie, _ := s.Auth.NewSession(ctx, admin.ID)
	bob, _, _ := s.Auth.CreateUser(ctx, "bob", "")
	s.Ledger.AddGateway(usage.GatewayEvent{RequestID: "r1", At: time.Now(), UserID: bob.ID, UserName: "bob", Status: 200})
	if rec := do(h, "DELETE", fmt.Sprintf("/api/admin/users/%d", bob.ID), "", adminCookie); rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	ctxDone, cancel := context.WithCancel(ctx)
	cancel()
	s.Ledger.Run(ctxDone) // writes what's queued, then returns

	// A new bob starts with an empty history.
	newBob, temp, _ := s.Auth.CreateUser(ctx, "bob", "")
	cookie, _ := s.Auth.NewSession(ctx, newBob.ID)
	s.Auth.ChangePassword(ctx, newBob.ID, temp, "bob's new password", cookie)
	rec := do(h, "GET", "/api/me/usage", "", cookie)
	var mine struct {
		Summary struct{ Totals struct{ Requests int } }
		Recent  []any
		Monthly []any
	}
	json.Unmarshal(rec.Body.Bytes(), &mine)
	if rec.Code != 200 || mine.Summary.Totals.Requests != 0 || len(mine.Recent) != 0 || len(mine.Monthly) != 0 {
		t.Errorf("the new bob sees the old one's usage: %d %s", rec.Code, rec.Body)
	}
	// The admin still sees it, under the deleted user's label.
	rec = do(h, "GET", "/api/admin/usage", "", adminCookie)
	if !strings.Contains(rec.Body.String(), `"bob (deleted `) {
		t.Errorf("the deleted user's usage isn't under their label: %s", rec.Body)
	}
}

func TestOriginGuard(t *testing.T) {
	s, h := newServer(t, settings.Settings{})
	admin, _ := s.Auth.CreateAdmin(t.Context(), "admin", "correct horse battery")
	cookie, _ := s.Auth.NewSession(t.Context(), admin.ID)
	rec := do(h, "POST", "/api/admin/users", `{"username":"eve"}`, cookie, "Origin", "http://evil.test")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site write accepted: %d", rec.Code)
	}
	if _, err := s.Auth.UserByName(t.Context(), "eve"); err == nil {
		t.Fatal("user created from another origin")
	}
}

func TestOriginGuardBehindProxy(t *testing.T) {
	cfg := settings.Settings{Auth: settings.AuthProxy, Proxy: settings.ProxySettings{
		Secret: "a-long-shared-secret", UserHeader: "X-Remote-User", RoleHeader: "X-Remote-Role",
		AdminRoles: []string{"admin"},
	}}
	_, h := newServer(t, cfg)
	refused := func(rec *httptest.ResponseRecorder) bool {
		return rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), "didn't come from this site")
	}
	// Past the guard, this endpoint answers that tokens come from the proxy.
	reached := func(rec *httptest.ResponseRecorder) bool {
		return strings.Contains(rec.Body.String(), "tokens come from that proxy")
	}
	// The browser uses the proxy's name; the proxy reaches YardMaster as ym.test.
	write := func(headers ...string) *httptest.ResponseRecorder {
		base := []string{"X-Remote-User", "alice", "X-Remote-Role", "admin", "Origin", "https://ai.example.com"}
		return do(h, "POST", "/api/me/tokens", `{"name":"x"}`, "", append(base, headers...)...)
	}
	if rec := write(auth.ProxySecretHeader, "a-long-shared-secret", "X-Forwarded-Host", "ai.example.com"); !reached(rec) {
		t.Fatalf("write through the trusted proxy refused: %s", rec.Body)
	}
	if rec := write(auth.ProxySecretHeader, "a-long-shared-secret", "X-Forwarded-Host", "ai.example.com, other.test"); !reached(rec) {
		t.Fatalf("first forwarded host not used: %s", rec.Body)
	}
	if rec := write(auth.ProxySecretHeader, "a-long-shared-secret", "X-Forwarded-Host", "other.test"); !refused(rec) {
		t.Fatalf("origin that matches neither host accepted: %d", rec.Code)
	}
	// X-Forwarded-Host counts only from the trusted proxy.
	if rec := write("X-Forwarded-Host", "ai.example.com"); !refused(rec) {
		t.Fatalf("forwarded host believed without the secret: %d", rec.Code)
	}
}

func TestOriginGuardIgnoresForwardedHostWithBuiltinSignIn(t *testing.T) {
	s, h := newServer(t, settings.Settings{})
	admin, _ := s.Auth.CreateAdmin(t.Context(), "admin", "correct horse battery")
	cookie, _ := s.Auth.NewSession(t.Context(), admin.ID)
	rec := do(h, "POST", "/api/admin/users", `{"username":"eve"}`, cookie, "Origin", "http://evil.test", "X-Forwarded-Host", "evil.test")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("forwarded host believed in built-in mode: %d", rec.Code)
	}
}

func TestConnectUsesTheBrowsersHost(t *testing.T) {
	origin := func(rec *httptest.ResponseRecorder) string {
		var out struct {
			OpenAIBase string `json:"openai_base"`
		}
		json.Unmarshal(rec.Body.Bytes(), &out)
		return out.OpenAIBase
	}
	cfg := settings.Settings{Auth: settings.AuthProxy, TLS: settings.TLSOff, Proxy: settings.ProxySettings{
		Secret: "a-long-shared-secret", UserHeader: "X-Remote-User", RoleHeader: "X-Remote-Role",
	}}
	_, h := newServer(t, cfg)
	// Behind the trusted proxy, the guide uses the name and scheme the browser used.
	rec := do(h, "GET", "/api/connect", "", "", "X-Remote-User", "alice", auth.ProxySecretHeader, "a-long-shared-secret",
		"X-Forwarded-Host", "ai.example.com", "X-Forwarded-Proto", "https")
	if got := origin(rec); got != "https://ai.example.com/v1" {
		t.Errorf("behind the proxy: %q (%d %s)", got, rec.Code, rec.Body)
	}
	// Without X-Forwarded-Host, it falls back to the Host header.
	rec = do(h, "GET", "/api/connect", "", "", "X-Remote-User", "alice", auth.ProxySecretHeader, "a-long-shared-secret")
	if got := origin(rec); got != "http://ym.test/v1" {
		t.Errorf("no forwarded host: %q", got)
	}

	// With built-in sign-in, X-Forwarded-Host is ignored.
	s, h := newServer(t, settings.Settings{TLS: settings.TLSOff})
	admin, _ := s.Auth.CreateAdmin(t.Context(), "admin", "correct horse battery")
	cookie, _ := s.Auth.NewSession(t.Context(), admin.ID)
	rec = do(h, "GET", "/api/connect", "", cookie, "X-Forwarded-Host", "evil.test")
	if got := origin(rec); got != "http://ym.test/v1" {
		t.Errorf("built-in sign-in: %q", got)
	}
}

func TestSessionCookieDoesntWorkOnGateway(t *testing.T) {
	s, h := newServer(t, settings.Settings{})
	admin, _ := s.Auth.CreateAdmin(t.Context(), "admin", "correct horse battery")
	cookie, _ := s.Auth.NewSession(t.Context(), admin.ID)
	if rec := do(h, "POST", "/v1/chat/completions", `{"model":"x"}`, cookie); rec.Code != http.StatusUnauthorized {
		t.Fatalf("session cookie accepted by the gateway: %d", rec.Code)
	}
}

func TestTrustedProxyMode(t *testing.T) {
	cfg := settings.Settings{Auth: settings.AuthProxy, Proxy: settings.ProxySettings{
		Secret: "a-long-shared-secret", UserHeader: "X-Remote-User", RoleHeader: "X-Remote-Role",
		AdminRoles: []string{"admin"}, Addresses: []netip.Prefix{netip.MustParsePrefix("10.9.0.0/16")},
	}}
	_, h := newServer(t, cfg)
	// Headers without the secret, from an unlisted address, are ignored.
	if rec := do(h, "GET", "/api/admin/users", "", "", "X-Remote-User", "mallory", "X-Remote-Role", "admin"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("forged identity accepted: %d", rec.Code)
	}
	// With the secret, the proxy's user and role apply.
	rec := do(h, "GET", "/api/admin/users", "", "", "X-Remote-User", "matt", "X-Remote-Role", "admin", auth.ProxySecretHeader, "a-long-shared-secret")
	if rec.Code != 200 {
		t.Fatalf("trusted admin refused: %d", rec.Code)
	}
	// A role that isn't mapped to admin is a plain user.
	rec = do(h, "GET", "/api/admin/users", "", "", "X-Remote-User", "bob", "X-Remote-Role", "staff", auth.ProxySecretHeader, "a-long-shared-secret")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unmapped role got admin: %d", rec.Code)
	}
	// The gateway requires the secret; a configured address alone isn't enough.
	req := httptest.NewRequest("POST", "http://ym.test/v1/chat/completions", strings.NewReader(`{"model":"x"}`))
	req.RemoteAddr = "10.9.0.5:5555"
	req.Header.Set("X-Remote-User", "bob")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("gateway accepted a request without the secret: %d", rec.Code)
	}
}

func TestDeactivatedProxyUserIsShutOut(t *testing.T) {
	cfg := settings.Settings{Auth: settings.AuthProxy, Proxy: settings.ProxySettings{
		Secret: "a-long-shared-secret", UserHeader: "X-Remote-User", RoleHeader: "X-Remote-Role",
		AdminRoles: []string{"admin"},
	}}
	s, h := newServer(t, cfg)
	as := func(user, role string) []string {
		return []string{"X-Remote-User", user, "X-Remote-Role", role, auth.ProxySecretHeader, "a-long-shared-secret"}
	}
	if rec := do(h, "GET", "/api/me/tokens", "", "", as("dave", "staff")...); rec.Code != 200 {
		t.Fatalf("first visit: %d %s", rec.Code, rec.Body)
	}
	dave, _ := s.Auth.UserByName(t.Context(), "dave")
	rec := do(h, "POST", fmt.Sprintf("/api/admin/users/%d/active", dave.ID), `{"active":false}`, "", as("matt", "admin")...)
	if rec.Code != 200 {
		t.Fatalf("deactivate: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/me/tokens", "", "", as("dave", "staff")...); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), "deactivated") {
		t.Errorf("UI after deactivation: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", "/v1/chat/completions", `{"model":"x"}`, "", as("dave", "staff")...); rec.Code != http.StatusUnauthorized ||
		!strings.Contains(rec.Body.String(), "deactivated") {
		t.Errorf("gateway after deactivation: %d %s", rec.Code, rec.Body)
	}
}

func TestForwardedHeadersTrustedLikeIdentity(t *testing.T) {
	cfg := settings.Settings{Auth: settings.AuthProxy, Proxy: settings.ProxySettings{
		Secret: "a-long-shared-secret", Addresses: []netip.Prefix{netip.MustParsePrefix("10.9.0.0/16")},
	}}
	s, _ := newServer(t, cfg)
	req := func(remote string, headers ...string) *http.Request {
		r := httptest.NewRequest("GET", "http://ym.test/api/session", nil)
		r.RemoteAddr = remote
		r.Header.Set("X-Forwarded-For", "192.0.2.7, 10.9.0.5")
		r.Header.Set("X-Forwarded-Proto", "https")
		for i := 0; i+1 < len(headers); i += 2 {
			r.Header.Set(headers[i], headers[i+1])
		}
		return r
	}
	// A proxy trusted by its address or by the secret reports the client's address and scheme.
	for name, r := range map[string]*http.Request{
		"address": req("10.9.0.5:5555"),
		"secret":  req("203.0.113.1:5555", auth.ProxySecretHeader, "a-long-shared-secret"),
	} {
		if got := s.clientIP(r); got != "192.0.2.7" {
			t.Errorf("%s: client IP %q", name, got)
		}
		if !s.isHTTPS(r) {
			t.Errorf("%s: forwarded https ignored", name)
		}
	}
	// Anyone else's X-Forwarded-* headers are ignored.
	r := req("203.0.113.1:5555")
	if got := s.clientIP(r); got != "203.0.113.1" {
		t.Errorf("untrusted: client IP %q", got)
	}
	if s.isHTTPS(r) {
		t.Error("untrusted: forwarded https believed")
	}
}
