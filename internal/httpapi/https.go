package httpapi

import (
	"context"
	_ "embed"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"

	"yardmaster/internal/audit"
	"yardmaster/internal/auth"
	"yardmaster/internal/settings"
	"yardmaster/internal/tlsca"
)

// HTTPS state, stored under the "https" setting. HTTPS with the built-in CA starts
// only once an admin's browser has confirmed the name reaches this YardMaster, so nobody
// is locked out by a name that doesn't resolve.
type httpsState struct {
	Name      string `json:"name"`
	Confirmed bool   `json:"confirmed"`
}

const settingHTTPS = "https"

// httpsState returns the stored HTTPS state. It's read from the database once and then
// kept in memory: port 8080 asks for it on every request, and only setHTTPSState changes it.
func (s *Server) httpsState(ctx context.Context) httpsState {
	s.httpsMu.Lock()
	defer s.httpsMu.Unlock()
	if !s.httpsLoaded {
		var st httpsState
		if _, err := s.DB.GetSetting(ctx, settingHTTPS, &st); err != nil {
			slog.Error("reading the HTTPS state", "err", err)
			return httpsState{}
		}
		s.https, s.httpsLoaded = st, true
	}
	return s.https
}

// setHTTPSState stores the HTTPS state.
func (s *Server) setHTTPSState(ctx context.Context, st httpsState) error {
	s.httpsMu.Lock()
	defer s.httpsMu.Unlock()
	if err := s.DB.SetSetting(ctx, settingHTTPS, st); err != nil {
		return err
	}
	s.https, s.httpsLoaded = st, true
	return nil
}

// httpsActive reports whether YardMaster serves HTTPS on its HTTPS port, and the name
// people should use.
func (s *Server) httpsActive(ctx context.Context) (bool, string) {
	if s.Settings.TLS == settings.TLSOff || !s.TLS.Ready() {
		return false, ""
	}
	if s.TLS.Mode() == tlsca.ModeCustom {
		return true, s.TLS.CustomName()
	}
	st := s.httpsState(ctx)
	return st.Confirmed && st.Name == s.TLS.CAName(), st.Name
}

// httpsURL is YardMaster's address over HTTPS under name.
func (s *Server) httpsURL(name string) string {
	return "https://" + name + ":" + strconv.Itoa(s.Settings.HTTPSPort)
}

// HTTPSActive is used by main to decide whether to serve the HTTPS port.
func (s *Server) HTTPSActive(ctx context.Context) bool {
	active, _ := s.httpsActive(ctx)
	return active
}

// SeedHTTPSName sets the name from YARDMASTER_PUBLIC_HOST on first start.
func (s *Server) SeedHTTPSName(ctx context.Context) {
	if s.Settings.TLS == settings.TLSOff || s.TLS.Mode() == tlsca.ModeCustom || s.Settings.PublicHost == "" {
		return
	}
	if st := s.httpsState(ctx); st.Name != "" {
		return
	}
	if _, err := s.TLS.SetName(s.Settings.PublicHost); err == nil {
		s.setHTTPSState(ctx, httpsState{Name: s.Settings.PublicHost})
	}
}

func (s *Server) handleHTTPS(w http.ResponseWriter, r *http.Request, u *auth.User) {
	active, name := s.httpsActive(r.Context())
	st := s.httpsState(r.Context())
	out := map[string]any{
		"enabled":     s.Settings.TLS != settings.TLSOff,
		"proxy_mode":  s.Settings.Auth == settings.AuthProxy,
		"mode":        s.TLS.Mode(),
		"active":      active,
		"name":        name,
		"pending":     st.Name != "" && !st.Confirmed && s.TLS.Mode() == tlsca.ModeBuiltin,
		"fingerprint": s.TLS.Fingerprint(),
		"https_port":  s.Settings.HTTPSPort,
		"http_port":   s.Settings.HTTPPort,
	}
	if na := s.TLS.NotAfter(); !na.IsZero() {
		out["not_after"] = na.Unix()
	}
	if out["name"] == "" {
		out["name"] = st.Name
	}
	writeJSON(w, http.StatusOK, out)
}

// handleHTTPSName sets the name and creates the CA for it. HTTPS doesn't start until the
// name is confirmed.
func (s *Server) handleHTTPSName(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var in struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if s.Settings.TLS == settings.TLSOff {
		writeError(w, http.StatusBadRequest, "HTTPS is turned off (YARDMASTER_TLS=off, or YardMaster runs behind another proxy)")
		return
	}
	newCA, err := s.TLS.SetName(in.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	st := s.httpsState(r.Context())
	if st.Name != in.Name {
		st = httpsState{Name: in.Name}
	}
	if err := s.setHTTPSState(r.Context(), st); err != nil {
		logErr(w, err)
		return
	}
	detail := "name " + in.Name
	if newCA {
		detail += ", new certificate authority " + s.TLS.Fingerprint()
	}
	s.record(r, u, audit.HTTPSChanged, detail)
	s.handleHTTPS(w, r, u)
}

// handleHTTPSConfirm is called by the admin's browser after it fetched
// http://<name>:<port>/api/instance-id and got this instance's ID back.
func (s *Server) handleHTTPSConfirm(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var in struct {
		Name       string `json:"name"`
		InstanceID string `json:"instance_id"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	st := s.httpsState(r.Context())
	if st.Name == "" || st.Name != in.Name {
		writeError(w, http.StatusBadRequest, "set the name first")
		return
	}
	if in.InstanceID != s.instanceID {
		writeError(w, http.StatusBadRequest, "that name reaches a different server, not this YardMaster")
		return
	}
	st.Confirmed = true
	if err := s.setHTTPSState(r.Context(), st); err != nil {
		logErr(w, err)
		return
	}
	s.record(r, u, audit.HTTPSChanged, "HTTPS on for "+st.Name)
	s.handleHTTPS(w, r, u)
}

// handleInstanceID answers any origin with this instance's random ID and nothing else, so
// a browser can check that a name reaches this YardMaster.
func (s *Server) handleInstanceID(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	writeJSON(w, http.StatusOK, map[string]string{"id": s.instanceID})
}

func (s *Server) handleCACert(w http.ResponseWriter, r *http.Request) {
	pem := s.TLS.CAPEM()
	if pem == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="yardmaster-ca.crt"`)
	w.Write(pem)
}

// setupPage is the plain-HTTP page that helps people move to HTTPS.
//
//go:embed setup.html
var setupHTML string

var setupPage = template.Must(template.New("setup").Parse(setupHTML))

func (s *Server) handleSetupPage(w http.ResponseWriter, r *http.Request) {
	active, name := s.httpsActive(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	setupPage.Execute(w, map[string]any{
		"Active":      active,
		"BuiltinCA":   s.TLS.Mode() == tlsca.ModeBuiltin,
		"Fingerprint": s.TLS.Fingerprint(),
		"URL":         s.httpsURL(name),
		"HTTPPort":    s.Settings.HTTPPort,
	})
}
