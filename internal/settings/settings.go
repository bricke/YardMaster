// Package settings reads YardMaster's environment variables into one typed struct.
//
// Every YARDMASTER_* variable is read here and nowhere else, so this file is the
// complete list of what can be configured from the container environment.
package settings

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Auth modes.
const (
	AuthBuiltin = "builtin"
	AuthProxy   = "proxy"
)

// TLS modes. "auto" uses the admin's own certificate if one is mounted, else the
// built-in certificate authority; "off" serves plain HTTP only.
const (
	TLSAuto = "auto"
	TLSOff  = "off"
)

type Settings struct {
	DataDir string

	HTTPPort  int
	HTTPSPort int

	// AdminPassword seeds the admin account on first start. Empty means the
	// first-run setup screen, gated by a code printed to the log.
	AdminPassword string
	// SecretKey encrypts UI-set provider keys at rest when set.
	SecretKey string
	// PublicHost seeds the name used for the certificate and connection guides.
	PublicHost string
	TLS        string

	Auth  string
	Proxy ProxySettings

	SwitchyardBin     string
	SwitchyardPort    int
	SwitchyardVersion string
	ShutdownTimeout   time.Duration

	UsageRetentionDays int
	AuditRetentionDays int
}

// ProxySettings configure trusted-proxy mode.
type ProxySettings struct {
	Secret     string
	Addresses  []netip.Prefix
	UserHeader string
	RoleHeader string
	AdminRoles []string
}

// Load reads the environment. It returns an error for values that are present but
// invalid, so a typo stops the container at start instead of being ignored.
func Load() (Settings, error) {
	s := Settings{
		DataDir:           env("YARDMASTER_DATA", "/data"),
		AdminPassword:     os.Getenv("YARDMASTER_ADMIN_PASSWORD"),
		SecretKey:         os.Getenv("YARDMASTER_SECRET_KEY"),
		PublicHost:        strings.TrimSpace(os.Getenv("YARDMASTER_PUBLIC_HOST")),
		TLS:               env("YARDMASTER_TLS", TLSAuto),
		Auth:              env("YARDMASTER_AUTH", AuthBuiltin),
		SwitchyardBin:     env("YARDMASTER_SWITCHYARD_BIN", "switchyard-server"),
		SwitchyardVersion: env("YARDMASTER_SWITCHYARD_VERSION", "unknown"),
		Proxy: ProxySettings{
			Secret:     os.Getenv("YARDMASTER_PROXY_SECRET"),
			UserHeader: env("YARDMASTER_PROXY_USER_HEADER", "X-Remote-User"),
			RoleHeader: env("YARDMASTER_PROXY_ROLE_HEADER", "X-Remote-Role"),
			AdminRoles: list(env("YARDMASTER_PROXY_ADMIN_ROLES", "admin")),
		},
	}
	var err error
	if s.HTTPPort, err = intEnv("YARDMASTER_HTTP_PORT", 8080); err != nil {
		return s, err
	}
	if s.HTTPSPort, err = intEnv("YARDMASTER_HTTPS_PORT", 8443); err != nil {
		return s, err
	}
	if s.SwitchyardPort, err = intEnv("YARDMASTER_SWITCHYARD_PORT", 4000); err != nil {
		return s, err
	}
	if s.UsageRetentionDays, err = intEnv("YARDMASTER_USAGE_RETENTION_DAYS", 90); err != nil {
		return s, err
	}
	if s.AuditRetentionDays, err = intEnv("YARDMASTER_AUDIT_RETENTION_DAYS", 365); err != nil {
		return s, err
	}
	s.ShutdownTimeout = 30 * time.Second
	if v := os.Getenv("YARDMASTER_SHUTDOWN_TIMEOUT"); v != "" {
		if s.ShutdownTimeout, err = time.ParseDuration(v); err != nil || s.ShutdownTimeout < time.Second {
			return s, fmt.Errorf("YARDMASTER_SHUTDOWN_TIMEOUT: want a duration of at least 1s, like 30s")
		}
	}
	for _, a := range list(os.Getenv("YARDMASTER_PROXY_ADDRESSES")) {
		p, err := parsePrefix(a)
		if err != nil {
			return s, fmt.Errorf("YARDMASTER_PROXY_ADDRESSES: %q is not an IP address or CIDR range", a)
		}
		s.Proxy.Addresses = append(s.Proxy.Addresses, p)
	}

	switch s.Auth {
	case AuthBuiltin:
	case AuthProxy:
		if s.Proxy.Secret == "" {
			return s, fmt.Errorf("YARDMASTER_AUTH=proxy needs YARDMASTER_PROXY_SECRET")
		}
		if len(s.Proxy.Secret) < 16 {
			return s, fmt.Errorf("YARDMASTER_PROXY_SECRET must be at least 16 characters")
		}
		// Behind another proxy, TLS is the proxy's job.
		s.TLS = TLSOff
	default:
		return s, fmt.Errorf("YARDMASTER_AUTH: want %q or %q, got %q", AuthBuiltin, AuthProxy, s.Auth)
	}
	if s.TLS != TLSAuto && s.TLS != TLSOff {
		return s, fmt.Errorf("YARDMASTER_TLS: want %q or %q, got %q", TLSAuto, TLSOff, s.TLS)
	}
	s.DataDir = filepath.Clean(s.DataDir)
	return s, nil
}

// Paths inside the data directory (see docs/architecture.md, "Data on disk").
func (s Settings) DBPath() string         { return filepath.Join(s.DataDir, "yardmaster.db") }
func (s Settings) SecretsDir() string     { return filepath.Join(s.DataDir, "secrets") }
func (s Settings) SwitchyardDir() string  { return filepath.Join(s.DataDir, "switchyard") }
func (s Settings) ConfigPath() string     { return filepath.Join(s.SwitchyardDir(), "config.toml") }
func (s Settings) HistoryDir() string     { return filepath.Join(s.SwitchyardDir(), "history") }
func (s Settings) RoutingLogPath() string { return filepath.Join(s.SwitchyardDir(), "routing.jsonl") }
func (s Settings) TLSDir() string         { return filepath.Join(s.DataDir, "tls") }

func env(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func intEnv(name string, fallback int) (int, error) {
	v := os.Getenv(name)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s: want a positive number, got %q", name, v)
	}
	return n, nil
}

func list(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parsePrefix(v string) (netip.Prefix, error) {
	if strings.Contains(v, "/") {
		return netip.ParsePrefix(v)
	}
	a, err := netip.ParseAddr(v)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(a, a.BitLen()), nil
}
