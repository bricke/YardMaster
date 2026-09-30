// Package httpapi serves the web UI, its /api, the plain-HTTP setup page and the /v1
// gateway. Handlers are thin: they check the role, call one package and write the audit
// entry. Roles are enforced here, never only by hiding UI.
package httpapi

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"yardmaster/internal/audit"
	"yardmaster/internal/auth"
	"yardmaster/internal/deploy"
	"yardmaster/internal/gateway"
	"yardmaster/internal/metrics"
	"yardmaster/internal/secrets"
	"yardmaster/internal/settings"
	"yardmaster/internal/store"
	"yardmaster/internal/supervisor"
	"yardmaster/internal/tlsca"
	"yardmaster/internal/usage"
)

//go:embed all:dist
var dist embed.FS

const sessionCookie = "ym_session"

// Deps are the packages the API calls.
type Deps struct {
	Settings settings.Settings
	Version  string
	DB       *store.DB
	Auth     *auth.Service
	Audit    *audit.Log
	Keys     *secrets.Store
	Sup      *supervisor.Supervisor
	Applier  *deploy.Applier
	Ledger   *usage.Ledger
	Prices   *usage.Prices
	Metrics  *metrics.Scraper
	TLS      *tlsca.Manager
}

type Server struct {
	Deps
	gateway    *gateway.Gateway
	static     fs.FS
	instanceID string

	setupMu   sync.Mutex
	setupCode string // first-run code printed to the log; "" once used
}

func New(d Deps) *Server {
	s := &Server{Deps: d}
	s.gateway = gateway.New(d.Sup.URL(), s.identifyGatewayCaller, d.Ledger)
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	s.static = sub
	b := make([]byte, 16)
	rand.Read(b)
	s.instanceID = hex.EncodeToString(b)
	return s
}

// SetSetupCode arms the first-run screen with a one-time code.
func (s *Server) SetSetupCode(code string) {
	s.setupMu.Lock()
	s.setupCode = code
	s.setupMu.Unlock()
}

// Handler is the full application: UI, API and gateway.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Open endpoints.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/instance-id", s.handleInstanceID)
	mux.HandleFunc("GET /ca.crt", s.handleCACert)
	mux.HandleFunc("GET /setup", s.handleSetupPage)

	// Sign-in.
	mux.HandleFunc("GET /api/session", s.handleSession)
	mux.HandleFunc("POST /api/first-run", s.handleFirstRun)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.Handle("POST /api/password", s.user(s.handleChangePassword, true))

	// Every signed-in user.
	mux.Handle("GET /api/status", s.user(s.handleStatus, false))
	mux.Handle("GET /api/connect", s.user(s.handleConnect, false))
	mux.Handle("GET /api/me/tokens", s.user(s.handleMyTokens, false))
	mux.Handle("POST /api/me/tokens", s.user(s.handleCreateToken, false))
	mux.Handle("DELETE /api/me/tokens/{id}", s.user(s.handleRevokeToken, false))
	mux.Handle("GET /api/me/usage", s.user(s.handleMyUsage, false))

	// Admin only.
	admin := func(pattern string, h func(http.ResponseWriter, *http.Request, *auth.User)) {
		mux.Handle(pattern, s.admin(h))
	}
	admin("GET /api/admin/overview", s.handleOverview)
	admin("GET /api/admin/usage", s.handleAdminUsage)
	admin("GET /api/admin/users", s.handleUsers)
	admin("POST /api/admin/users", s.handleCreateUser)
	admin("POST /api/admin/users/{id}/reset-password", s.handleResetPassword)
	admin("POST /api/admin/users/{id}/active", s.handleSetActive)
	admin("DELETE /api/admin/users/{id}", s.handleDeleteUser)
	admin("GET /api/admin/deployment", s.handleDeployment)
	admin("PUT /api/admin/deployment/model", s.handleSaveModel)
	admin("POST /api/admin/deployment/preview", s.handlePreview)
	admin("POST /api/admin/deployment/apply", s.handleApply)
	admin("GET /api/admin/deployment/history", s.handleHistory)
	admin("GET /api/admin/deployment/history/{id}", s.handleHistoryConfig)
	admin("GET /api/admin/keys", s.handleKeys)
	admin("PUT /api/admin/keys/{name}", s.handleSetKey)
	admin("DELETE /api/admin/keys/{name}", s.handleDeleteKey)
	admin("GET /api/admin/switchyard", s.handleSwitchyard)
	admin("POST /api/admin/switchyard/restart", s.handleRestart)
	admin("POST /api/admin/switchyard/stats-reset", s.handleStatsReset)
	admin("POST /api/admin/playground", s.handlePlayground)
	admin("GET /api/admin/prices", s.handlePrices)
	admin("PUT /api/admin/prices/{target}", s.handleSetPrice)
	admin("DELETE /api/admin/prices/{target}", s.handleDeletePrice)
	admin("GET /api/admin/audit", s.handleAudit)
	admin("GET /api/admin/https", s.handleHTTPS)
	admin("POST /api/admin/https/name", s.handleHTTPSName)
	admin("POST /api/admin/https/confirm", s.handleHTTPSConfirm)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "unknown API endpoint")
	})
	mux.Handle("/v1/", s.gateway)
	mux.HandleFunc("/", s.handleStatic)

	return s.securityHeaders(s.originGuard(mux))
}

// PlainHandler serves port 8080. Until HTTPS is active it is the full application. Once
// it is, 8080 only helps people move to HTTPS: the setup page, the CA certificate and the
// name check. Gateway and API requests get an error, never a redirect, because clients
// drop credentials on a redirect and the token has already crossed the network in clear.
func (s *Server) PlainHandler(full http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		active, name := s.httpsActive(r.Context())
		if !active {
			full.ServeHTTP(w, r)
			return
		}
		switch {
		case r.URL.Path == "/health", r.URL.Path == "/setup", r.URL.Path == "/ca.crt",
			r.URL.Path == "/api/instance-id":
			full.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/v1/"), strings.HasPrefix(r.URL.Path, "/api/"):
			msg := "YardMaster now uses HTTPS. Use " + s.httpsURL(name) +
				" and, since this request was sent unencrypted, replace the token or password it carried."
			writeError(w, http.StatusForbidden, msg)
		default:
			http.Redirect(w, r, "/setup", http.StatusFound)
		}
	})
}

// ---- identity ----

// currentUser resolves the signed-in user: a session cookie, or in trusted-proxy mode
// the identity the proxy vouches for.
func (s *Server) currentUser(r *http.Request) (*auth.User, error) {
	if s.Settings.Auth == settings.AuthProxy {
		return s.Auth.ProxyIdentity(r.Context(), r, s.Settings.Proxy, remoteAddr(r), true)
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil, auth.ErrNotFound
	}
	return s.Auth.Session(r.Context(), c.Value)
}

func (s *Server) identifyGatewayCaller(r *http.Request) (*auth.Caller, error) {
	if s.Settings.Auth == settings.AuthProxy {
		u, err := s.Auth.ProxyIdentity(r.Context(), r, s.Settings.Proxy, remoteAddr(r), false)
		if err != nil {
			return nil, errors.New("request not authorized by the trusted proxy")
		}
		return &auth.Caller{UserID: u.ID, Username: u.Username, TokenName: "via proxy"}, nil
	}
	token := gateway.Token(r)
	if token == "" {
		return nil, errors.New("missing API key: use a YardMaster token (ym_…) as your API key")
	}
	c, err := s.Auth.Authenticate(r.Context(), token)
	if err != nil {
		return nil, errors.New("invalid, expired or revoked YardMaster token")
	}
	return c, nil
}

// user wraps handlers for any signed-in user. allowTemp lets through users who still
// hold a temporary password, for the change-password call only.
func (s *Server) user(h func(http.ResponseWriter, *http.Request, *auth.User), allowTemp bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := s.currentUser(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "sign in first")
			return
		}
		if u.MustChangePassword && !allowTemp {
			writeError(w, http.StatusForbidden, "choose a new password first")
			return
		}
		h(w, r, u)
	})
}

func (s *Server) admin(h func(http.ResponseWriter, *http.Request, *auth.User)) http.Handler {
	return s.user(func(w http.ResponseWriter, r *http.Request, u *auth.User) {
		if u.Role != auth.RoleAdmin {
			writeError(w, http.StatusForbidden, "admins only")
			return
		}
		h(w, r, u)
	}, false)
}

// ---- middleware ----

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		if !strings.HasPrefix(r.URL.Path, "/v1/") {
			// connect-src allows http(s) for the HTTPS name check, which fetches
			// /api/instance-id through the name being confirmed.
			h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; "+
				"style-src 'self' 'unsafe-inline'; connect-src 'self' http: https:; frame-ancestors 'none'")
		}
		// No HSTS: it would also force HTTPS onto the plain-HTTP setup page on 8080 and on
		// any other plain-HTTP service sharing this host name.
		next.ServeHTTP(w, r)
	})
}

// originGuard refuses state-changing /api requests whose Origin isn't this site.
func (s *Server) originGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Method != http.MethodGet && r.Method != http.MethodHead {
			origin := r.Header.Get("Origin")
			u, err := url.Parse(origin)
			if origin == "" || err != nil || !s.isSiteHost(r, u.Host) {
				writeError(w, http.StatusForbidden, "request refused: it didn't come from this site")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// isSiteHost reports whether host is the name the browser used to reach YardMaster: the
// Host header or, behind the trusted proxy, the host it forwarded in X-Forwarded-Host.
func (s *Server) isSiteHost(r *http.Request, host string) bool {
	if strings.EqualFold(host, r.Host) {
		return true
	}
	if s.Settings.Auth != settings.AuthProxy || !auth.ProxyTrusted(r, s.Settings.Proxy, remoteAddr(r), true) {
		return false
	}
	fwd := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0])
	return fwd != "" && strings.EqualFold(host, fwd)
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeOK(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// writeApplyError reports a failed apply or restart, with switchyard-server's last log
// lines, and reports whether err was one.
func writeApplyError(w http.ResponseWriter, err error) bool {
	var ae *deploy.ApplyError
	if !errors.As(err, &ae) {
		return false
	}
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": ae.Reason, "apply_error": ae})
	return true
}

// readJSON decodes a small JSON body into v.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "the request body isn't valid JSON")
		return false
	}
	return true
}

// pathID reads the {id} in the URL, or answers 400 naming what kind of id it expected.
func pathID(w http.ResponseWriter, r *http.Request, kind string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad "+kind+" id")
		return 0, false
	}
	return id, true
}

func remoteAddr(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, _ := netip.ParseAddr(host)
	return a
}

// clientIP is the address recorded in the audit log. Behind another proxy it's the
// address the proxy reports.
func (s *Server) clientIP(r *http.Request) string {
	if s.Settings.Auth == settings.AuthProxy && auth.HasProxySecret(r, s.Settings.Proxy.Secret) {
		if f := r.Header.Get("X-Forwarded-For"); f != "" {
			return strings.TrimSpace(strings.Split(f, ",")[0])
		}
	}
	return remoteAddr(r).String()
}

func (s *Server) record(r *http.Request, u *auth.User, action, detail string) {
	actor := ""
	if u != nil {
		actor = u.Username
	}
	s.Audit.Record(r.Context(), actor, s.clientIP(r), action, detail)
}

func queryInt(r *http.Request, name string, fallback int) int {
	if v, err := strconv.Atoi(r.URL.Query().Get(name)); err == nil {
		return v
	}
	return fallback
}

func logErr(w http.ResponseWriter, err error) {
	slog.Error("request failed", "err", err)
	writeError(w, http.StatusInternalServerError, "something went wrong; the details are in the container log")
}

// isHTTPS reports whether the browser reached us over HTTPS, directly or through another
// proxy that says so.
func (s *Server) isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return s.Settings.Auth == settings.AuthProxy && auth.HasProxySecret(r, s.Settings.Proxy.Secret) &&
		strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
