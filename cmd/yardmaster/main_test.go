package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"yardmaster/internal/audit"
	"yardmaster/internal/auth"
	"yardmaster/internal/deploy"
	"yardmaster/internal/httpapi"
	"yardmaster/internal/secrets"
	"yardmaster/internal/settings"
	"yardmaster/internal/store"
	"yardmaster/internal/supervisor"
)

func TestHealthcheck(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("checked %s, want /health", r.URL.Path)
		}
		w.WriteHeader(status)
	}))
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	t.Setenv("YARDMASTER_HTTP_PORT", port)

	if err := healthcheck(); err != nil {
		t.Errorf("healthy server: %v", err)
	}
	status = http.StatusServiceUnavailable
	if err := healthcheck(); err == nil {
		t.Error("a 503 passed the health check")
	}
	srv.Close()
	if err := healthcheck(); err == nil {
		t.Error("a closed port passed the health check")
	}
}

// withStdin runs f with os.Stdin reading input, as when a password is piped in.
func withStdin(t *testing.T, input string, f func() error) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdin")
	os.WriteFile(path, []byte(input), 0o600)
	in, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	old := os.Stdin
	os.Stdin = in
	defer func() { os.Stdin = old }()
	return f()
}

// captureStdout returns what f prints.
func captureStdout(t *testing.T, f func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	err = f()
	os.Stdout = old
	w.Close()
	out, _ := io.ReadAll(r)
	return string(out), err
}

func TestResetPassword(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("YARDMASTER_DATA", dir)
	db, err := store.Open(filepath.Join(dir, "yardmaster.db"))
	if err != nil {
		t.Fatal(err)
	}
	a := auth.New(db)
	admin, err := a.CreateAdmin(t.Context(), "admin", "old-password-1")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := a.NewSession(t.Context(), admin.ID)
	db.Close()

	if err := withStdin(t, "short\n", resetPassword); err == nil {
		t.Error("a too-short password was accepted")
	}
	out, err := captureStdout(t, func() error { return withStdin(t, "new-password-2\n", resetPassword) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Password for admin changed") {
		t.Errorf("output %q", out)
	}

	db, _ = store.Open(filepath.Join(dir, "yardmaster.db"))
	defer db.Close()
	a = auth.New(db)
	if _, err := a.Login(t.Context(), "admin", "new-password-2", "127.0.0.1"); err != nil {
		t.Errorf("new password doesn't work: %v", err)
	}
	if _, err := a.Login(t.Context(), "admin", "old-password-1", "127.0.0.2"); err == nil {
		t.Error("old password still works")
	}
	if _, err := a.Session(t.Context(), session); err == nil {
		t.Error("the admin's session survived the reset")
	}
	entries, _ := audit.New(db).Query(t.Context(), "", audit.AdminPasswordReset, 0, 10)
	if len(entries) != 1 {
		t.Error("the reset wasn't audited")
	}
}

func TestSwitchyardEnvPassesOnlyWhatSwitchyardNeeds(t *testing.T) {
	keys, err := secrets.Open(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := keys.Set("OPENROUTER_API_KEY", "sk-from-ui"); err != nil {
		t.Fatal(err)
	}
	keys.Set("UNUSED_API_KEY", "sk-not-in-config")
	t.Setenv("RUST_LOG", "info")
	for _, secret := range []string{"YARDMASTER_ADMIN_PASSWORD", "YARDMASTER_SECRET_KEY", "YARDMASTER_PROXY_SECRET"} {
		t.Setenv(secret, "must-not-leak")
	}

	d := &deploy.Deployment{
		Clients: []deploy.Client{{Name: "or", Format: "openai_chat", BaseURL: "https://openrouter.ai/api/v1", Auth: deploy.AuthKey, KeyEnv: "OPENROUTER_API_KEY"}},
		Targets: []deploy.Target{{Name: "t", ModelID: "m", Client: "or"}},
		Routes:  []deploy.Route{{Name: "p", ID: "fast", Type: deploy.RoutePassthrough, Target: "t"}},
	}
	text, err := d.TOML()
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(config, []byte(text), 0o600)

	env, err := switchyardEnv(keys)(config)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	for _, want := range []string{"OPENROUTER_API_KEY=sk-from-ui", "RUST_LOG=info", "HOME=/tmp"} {
		if !slices.Contains(env, want) {
			t.Errorf("env lacks %q:\n%s", want, joined)
		}
	}
	for _, leak := range []string{"must-not-leak", "UNUSED_API_KEY", "YARDMASTER_"} {
		if strings.Contains(joined, leak) {
			t.Errorf("env contains %q:\n%s", leak, joined)
		}
	}
}

func TestFirstAdmin(t *testing.T) {
	newServer := func() *httpapi.Server {
		return httpapi.New(httpapi.Deps{Sup: supervisor.New(supervisor.Options{Port: 1})})
	}
	setup := func(t *testing.T) (*auth.Service, *audit.Log) {
		db := store.OpenTest(t)
		return auth.New(db), audit.New(db)
	}

	t.Run("from the environment", func(t *testing.T) {
		a, log := setup(t)
		cfg := settings.Settings{Auth: settings.AuthBuiltin, AdminPassword: "env-password-1"}
		if err := firstAdmin(t.Context(), cfg, a, log, newServer()); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Login(t.Context(), "admin", "env-password-1", "127.0.0.1"); err != nil {
			t.Errorf("admin not created: %v", err)
		}
		// A second start must not touch the existing admin.
		cfg.AdminPassword = "other-password-2"
		if err := firstAdmin(t.Context(), cfg, a, log, newServer()); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Login(t.Context(), "admin", "env-password-1", "127.0.0.2"); err != nil {
			t.Errorf("existing admin's password changed: %v", err)
		}
	})

	t.Run("weak password from the environment", func(t *testing.T) {
		a, log := setup(t)
		cfg := settings.Settings{Auth: settings.AuthBuiltin, AdminPassword: "short"}
		err := firstAdmin(t.Context(), cfg, a, log, newServer())
		if err == nil || !strings.Contains(err.Error(), "YARDMASTER_ADMIN_PASSWORD") {
			t.Errorf("error %v, want one naming YARDMASTER_ADMIN_PASSWORD", err)
		}
	})

	t.Run("setup code", func(t *testing.T) {
		a, log := setup(t)
		cfg := settings.Settings{Auth: settings.AuthBuiltin}
		out, err := captureStdout(t, func() error { return firstAdmin(t.Context(), cfg, a, log, newServer()) })
		if err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(`setup code: [A-Z2-9]{4}-[A-Z2-9]{4}-[A-Z2-9]{4}\n`).MatchString(out) {
			t.Errorf("no setup code in %q", out)
		}
		if has, _ := a.HasAdmin(t.Context()); has {
			t.Error("an admin was created without a password")
		}
	})

	t.Run("behind another proxy", func(t *testing.T) {
		a, log := setup(t)
		cfg := settings.Settings{Auth: settings.AuthProxy}
		out, err := captureStdout(t, func() error { return firstAdmin(t.Context(), cfg, a, log, newServer()) })
		if err != nil || out != "" {
			t.Errorf("proxy mode: %q, %v", out, err)
		}
	})
}
