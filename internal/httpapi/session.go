package httpapi

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"yardmaster/internal/audit"
	"yardmaster/internal/auth"
	"yardmaster/internal/settings"
)

// handleSession tells the UI who is signed in and what to show first.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"auth_mode": s.Settings.Auth, "version": s.Version,
		"switchyard_version": s.Settings.SwitchyardVersion}
	if s.Settings.Auth == settings.AuthBuiltin {
		hasAdmin, err := s.Auth.HasAdmin(r.Context())
		if err != nil {
			logErr(w, err)
			return
		}
		out["first_run"] = !hasAdmin
	}
	u, err := s.currentUser(r)
	out["user"] = u
	// Behind another proxy, someone an admin deactivated here is still signed in there:
	// tell them, instead of asking them to sign in.
	out["deactivated"] = errors.Is(err, auth.ErrInactive)
	writeJSON(w, http.StatusOK, out)
}

// handleFirstRun creates the admin account, once, with the code from the container log.
func (s *Server) handleFirstRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SetupCode string `json:"setup_code"`
		Username  string `json:"username"`
		Password  string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if s.Settings.Auth != settings.AuthBuiltin {
		writeError(w, http.StatusNotFound, "not available behind another proxy")
		return
	}
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if s.setupCode == "" {
		writeError(w, http.StatusConflict, "the admin account already exists")
		return
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(in.SetupCode)), []byte(s.setupCode)) != 1 {
		s.Audit.Record(r.Context(), "", s.clientIP(r), audit.LoginFailed, "wrong first-run setup code")
		writeError(w, http.StatusForbidden, "that setup code is wrong; find it in the container log (docker logs)")
		return
	}
	u, err := s.Auth.CreateAdmin(r.Context(), in.Username, in.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.setupCode = ""
	s.record(r, u, audit.AdminCreated, "first-run setup")
	s.startSession(w, r, u)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if s.Settings.Auth != settings.AuthBuiltin {
		writeError(w, http.StatusNotFound, "sign in through the proxy in front of YardMaster")
		return
	}
	u, err := s.Auth.Login(r.Context(), in.Username, in.Password, s.clientIP(r))
	if err != nil {
		s.Audit.Record(r.Context(), in.Username, s.clientIP(r), audit.LoginFailed, err.Error())
		status := http.StatusUnauthorized
		if errors.Is(err, auth.ErrThrottled) || errors.Is(err, auth.ErrBusy) {
			status = http.StatusTooManyRequests
		}
		writeError(w, status, err.Error())
		return
	}
	s.record(r, u, audit.LoginOK, "")
	s.startSession(w, r, u)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u *auth.User) {
	token, err := s.Auth.NewSession(r.Context(), u.ID)
	if err != nil {
		logErr(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.isHTTPS(r),
		MaxAge: int(auth.SessionLifetime.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if u, err := s.Auth.Session(r.Context(), c.Value); err == nil {
			s.record(r, u, audit.Logout, "")
		}
		s.Auth.EndSession(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: s.isHTTPS(r)})
	writeOK(w)
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var in struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if u.Source != auth.SourceBuiltin {
		writeError(w, http.StatusBadRequest, "your password is managed by the proxy in front of YardMaster")
		return
	}
	keep := ""
	if c, err := r.Cookie(sessionCookie); err == nil {
		keep = c.Value
	}
	if err := s.Auth.ChangePassword(r.Context(), u.ID, in.Current, in.New, keep); err != nil {
		if errors.Is(err, auth.ErrBadCredentials) {
			writeError(w, http.StatusBadRequest, "the current password is wrong")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r, u, audit.PasswordChanged, "")
	nu, _ := s.Auth.UserByID(r.Context(), u.ID)
	writeJSON(w, http.StatusOK, map[string]any{"user": nu})
}
