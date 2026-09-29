package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/netip"
	"strings"

	"yardmaster/internal/settings"
)

// ProxySecretHeader carries the shared secret on UI requests from another proxy, whose
// own Authorization header may belong to the app. Gateway requests may use either this
// header or "Authorization: Bearer <secret>".
const ProxySecretHeader = "X-YardMaster-Proxy-Secret"

var ErrProxyUntrusted = errors.New("request did not come from the trusted proxy")

// ProxyIdentity reads who another proxy says is making the request.
//
// Identity headers are believed only when the request carries the shared secret, or, for
// UI requests (allowAddress), when it comes from a proxy address the admin configured.
// Nothing can grant admin except a role listed in YARDMASTER_PROXY_ADMIN_ROLES.
func (s *Service) ProxyIdentity(ctx context.Context, r *http.Request, cfg settings.ProxySettings, remote netip.Addr, allowAddress bool) (*User, error) {
	if !ProxyTrusted(r, cfg, remote, allowAddress) {
		return nil, ErrProxyUntrusted
	}
	name := strings.TrimSpace(r.Header.Get(cfg.UserHeader))
	if name == "" {
		return nil, ErrNotFound
	}
	role := RoleUser
	for _, got := range strings.Split(r.Header.Get(cfg.RoleHeader), ",") {
		for _, admin := range cfg.AdminRoles {
			if strings.EqualFold(strings.TrimSpace(got), admin) {
				role = RoleAdmin
			}
		}
	}
	return s.ProxyUser(ctx, name, role)
}

// ProxyTrusted reports whether r came from the trusted proxy: it carries the shared secret
// or, when allowAddress is set, it comes from a configured proxy address.
func ProxyTrusted(r *http.Request, cfg settings.ProxySettings, remote netip.Addr, allowAddress bool) bool {
	if HasProxySecret(r, cfg.Secret) {
		return true
	}
	if allowAddress {
		for _, p := range cfg.Addresses {
			if p.Contains(remote.Unmap()) {
				return true
			}
		}
	}
	return false
}

// HasProxySecret reports whether r carries the shared proxy secret.
func HasProxySecret(r *http.Request, secret string) bool {
	if secret == "" {
		return false
	}
	got := r.Header.Get(ProxySecretHeader)
	if got == "" {
		got, _ = strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(secret)) == 1
}
