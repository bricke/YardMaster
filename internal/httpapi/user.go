package httpapi

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"

	"yardmaster/internal/audit"
	"yardmaster/internal/auth"
	"yardmaster/internal/deploy"
	"yardmaster/internal/settings"
	"yardmaster/internal/supervisor"
	"yardmaster/internal/tlsca"
	"yardmaster/internal/usage"
)

// handleStatus is the router badge every user sees: up, restarting or down.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request, u *auth.User) {
	state := s.Sup.Status().State
	switch state {
	case supervisor.StateStarting:
		state = supervisor.StateRestarting
	case supervisor.StateStopped, supervisor.StateUnconfigured:
		state = supervisor.StateDown
	}
	writeJSON(w, http.StatusOK, map[string]string{"state": state})
}

// handleConnect gives the agent setup guides what they need: addresses, route names and
// how to trust the certificate.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request, u *auth.User) {
	origin := s.publicOrigin(r)
	var routes []string
	strategies := []deploy.RouteStrategy{}
	if p := s.Applier.Parsed(); p != nil {
		routes = p.RouteIDs()
		strategies = p.RouteStrategies()
	}
	active, _ := s.httpsActive(r.Context())
	out := map[string]any{
		"origin":           origin,
		"openai_base":      origin + "/v1",
		"anthropic_base":   origin,
		"routes":           routes,
		"route_strategies": strategies,
		"https":            active || s.isHTTPS(r),
		"builtin_ca":       active && s.TLS.Mode() == tlsca.ModeBuiltin,
		"proxy_mode":       s.Settings.Auth == settings.AuthProxy,
	}
	if active && s.TLS.Mode() == tlsca.ModeBuiltin {
		out["ca_url"] = "http://" + hostOnly(r.Host) + ":" + strconv.Itoa(s.Settings.HTTPPort) + "/ca.crt"
		out["ca_fingerprint"] = s.TLS.Fingerprint()
	}
	writeJSON(w, http.StatusOK, out)
}

// publicOrigin is the address coworkers' tools should use.
func (s *Server) publicOrigin(r *http.Request) string {
	if active, name := s.httpsActive(r.Context()); active {
		return s.httpsURL(name)
	}
	scheme := "http"
	if s.isHTTPS(r) {
		scheme = "https"
	}
	host := r.Host
	if fwd := s.forwardedHost(r); fwd != "" {
		host = fwd
	}
	return scheme + "://" + host
}

func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

func (s *Server) handleMyTokens(w http.ResponseWriter, r *http.Request, u *auth.User) {
	tokens, err := s.Auth.Tokens(r.Context(), u.ID)
	if err != nil {
		logErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": tokens, "max": auth.MaxTokensPerUser})
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request, u *auth.User) {
	if s.Settings.Auth == settings.AuthProxy {
		writeError(w, http.StatusBadRequest, "behind another proxy, tokens come from that proxy")
		return
	}
	var in struct {
		Name          string `json:"name"`
		ExpiresInDays int    `json:"expires_in_days"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.ExpiresInDays < 0 || in.ExpiresInDays > 3650 {
		writeError(w, http.StatusBadRequest, "expiry must be between 0 (never) and 3650 days")
		return
	}
	t, value, err := s.Auth.CreateToken(r.Context(), u.ID, in.Name, in.ExpiresInDays)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r, u, audit.TokenCreated, fmt.Sprintf("%s (%s…)", t.Name, t.Hint))
	// The value is returned this once and never again.
	writeJSON(w, http.StatusOK, map[string]any{"token": t, "value": value})
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request, u *auth.User) {
	id, ok := pathID(w, r, "token")
	if !ok {
		return
	}
	t, err := s.Auth.RevokeToken(r.Context(), u.ID, id)
	if errors.Is(err, auth.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such token")
		return
	}
	if err != nil {
		logErr(w, err)
		return
	}
	s.record(r, u, audit.TokenRevoked, fmt.Sprintf("%s (%s…)", t.Name, t.Hint))
	writeJSON(w, http.StatusOK, map[string]any{"token": t})
}

// handleMyUsage is a user's own usage, per token. Nobody else's usage is ever
// included.
func (s *Server) handleMyUsage(w http.ResponseWriter, r *http.Request, u *auth.User) {
	f := usage.Filter{Days: queryInt(r, "days", 30), UserName: u.Username}
	summary, err := s.Ledger.Summarize(r.Context(), f)
	if err != nil {
		logErr(w, err)
		return
	}
	byToken, err := s.Ledger.ByToken(r.Context(), f)
	if err != nil {
		logErr(w, err)
		return
	}
	recent, err := s.Ledger.Recent(r.Context(), f, false, 50)
	if err != nil {
		logErr(w, err)
		return
	}
	for i := range recent {
		recent[i].UserName = ""
	}
	monthly, err := s.Ledger.Monthly(r.Context(), u.Username)
	if err != nil {
		logErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"summary": summary, "by_token": byToken, "recent": recent, "monthly": monthly,
	})
}
