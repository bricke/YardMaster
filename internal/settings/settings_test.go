package settings

import (
	"net/netip"
	"strings"
	"testing"
	"time"
)

// clearEnv unsets every variable Load reads, so the machine running the tests can't
// change the result. Load treats an empty value as unset.
func clearEnv(t *testing.T) {
	for _, name := range []string{
		"YARDMASTER_DATA", "YARDMASTER_ADMIN_PASSWORD", "YARDMASTER_SECRET_KEY", "YARDMASTER_PUBLIC_HOST",
		"YARDMASTER_TLS", "YARDMASTER_AUTH", "YARDMASTER_SWITCHYARD_BIN", "YARDMASTER_SWITCHYARD_VERSION",
		"YARDMASTER_PROXY_SECRET", "YARDMASTER_PROXY_USER_HEADER", "YARDMASTER_PROXY_ROLE_HEADER",
		"YARDMASTER_PROXY_ADMIN_ROLES", "YARDMASTER_PROXY_ADDRESSES", "YARDMASTER_HTTP_PORT",
		"YARDMASTER_HTTPS_PORT", "YARDMASTER_SWITCHYARD_PORT", "YARDMASTER_USAGE_RETENTION_DAYS",
		"YARDMASTER_AUDIT_RETENTION_DAYS", "YARDMASTER_SHUTDOWN_TIMEOUT", "YARDMASTER_JUDGE_PORT",
		"YARDMASTER_TYPESAFE_URL",
	} {
		t.Setenv(name, "")
	}
}

func TestDefaults(t *testing.T) {
	clearEnv(t)
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if s.DataDir != "/data" || s.HTTPPort != 8080 || s.HTTPSPort != 8443 || s.SwitchyardPort != 4000 || s.JudgePort != 4001 {
		t.Errorf("paths and ports: %+v", s)
	}
	if s.Auth != AuthBuiltin || s.TLS != TLSAuto || s.ShutdownTimeout != 30*time.Second {
		t.Errorf("modes: auth %q, tls %q, shutdown %v", s.Auth, s.TLS, s.ShutdownTimeout)
	}
	if s.UsageRetentionDays != 90 || s.AuditRetentionDays != 365 {
		t.Errorf("retention: %d, %d", s.UsageRetentionDays, s.AuditRetentionDays)
	}
	if s.Proxy.UserHeader != "X-Remote-User" || s.Proxy.RoleHeader != "X-Remote-Role" ||
		len(s.Proxy.AdminRoles) != 1 || s.Proxy.AdminRoles[0] != "admin" {
		t.Errorf("proxy: %+v", s.Proxy)
	}
	if s.ConfigPath() != "/data/switchyard/config.toml" || s.DBPath() != "/data/yardmaster.db" {
		t.Errorf("derived paths: %s, %s", s.ConfigPath(), s.DBPath())
	}
}

func TestValues(t *testing.T) {
	clearEnv(t)
	t.Setenv("YARDMASTER_DATA", "/srv/ym/")
	t.Setenv("YARDMASTER_HTTP_PORT", "9080")
	t.Setenv("YARDMASTER_SHUTDOWN_TIMEOUT", "2m")
	t.Setenv("YARDMASTER_PUBLIC_HOST", "  llm.example.com ")
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if s.DataDir != "/srv/ym" || s.HTTPPort != 9080 || s.ShutdownTimeout != 2*time.Minute || s.PublicHost != "llm.example.com" {
		t.Errorf("got %+v", s)
	}
}

func TestProxyMode(t *testing.T) {
	clearEnv(t)
	t.Setenv("YARDMASTER_AUTH", "proxy")
	t.Setenv("YARDMASTER_PROXY_SECRET", "0123456789abcdef")
	t.Setenv("YARDMASTER_PROXY_ADDRESSES", "10.0.0.5, 172.16.0.0/12,,")
	t.Setenv("YARDMASTER_PROXY_ADMIN_ROLES", " ops , admins ")
	t.Setenv("YARDMASTER_TLS", "auto")
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if s.TLS != TLSOff {
		t.Errorf("TLS %q: behind another proxy it should be off", s.TLS)
	}
	want := []netip.Prefix{netip.MustParsePrefix("10.0.0.5/32"), netip.MustParsePrefix("172.16.0.0/12")}
	if len(s.Proxy.Addresses) != 2 || s.Proxy.Addresses[0] != want[0] || s.Proxy.Addresses[1] != want[1] {
		t.Errorf("addresses %v, want %v", s.Proxy.Addresses, want)
	}
	if strings.Join(s.Proxy.AdminRoles, "|") != "ops|admins" {
		t.Errorf("admin roles %q", s.Proxy.AdminRoles)
	}
}

func TestInvalidValuesStopTheStart(t *testing.T) {
	for _, c := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"port not a number", map[string]string{"YARDMASTER_HTTP_PORT": "http"}, "YARDMASTER_HTTP_PORT"},
		{"port zero", map[string]string{"YARDMASTER_HTTPS_PORT": "0"}, "YARDMASTER_HTTPS_PORT"},
		{"negative retention", map[string]string{"YARDMASTER_USAGE_RETENTION_DAYS": "-1"}, "YARDMASTER_USAGE_RETENTION_DAYS"},
		{"shutdown not a duration", map[string]string{"YARDMASTER_SHUTDOWN_TIMEOUT": "30"}, "YARDMASTER_SHUTDOWN_TIMEOUT"},
		{"shutdown too short", map[string]string{"YARDMASTER_SHUTDOWN_TIMEOUT": "500ms"}, "YARDMASTER_SHUTDOWN_TIMEOUT"},
		{"unknown auth", map[string]string{"YARDMASTER_AUTH": "ldap"}, "YARDMASTER_AUTH"},
		{"unknown tls", map[string]string{"YARDMASTER_TLS": "on"}, "YARDMASTER_TLS"},
		{"proxy without secret", map[string]string{"YARDMASTER_AUTH": "proxy"}, "YARDMASTER_PROXY_SECRET"},
		{"proxy secret too short", map[string]string{"YARDMASTER_AUTH": "proxy", "YARDMASTER_PROXY_SECRET": "short"}, "at least 16"},
		{"bad proxy address", map[string]string{"YARDMASTER_PROXY_ADDRESSES": "proxy.local"}, "YARDMASTER_PROXY_ADDRESSES"},
	} {
		t.Run(c.name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %v, want one naming %q", err, c.want)
			}
		})
	}
}
