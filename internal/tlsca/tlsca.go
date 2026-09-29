// Package tlsca gives YardMaster HTTPS without outside accounts.
//
// By default it creates its own certificate authority, limited by name constraints to
// the one name YardMaster is reached by, with every IP address excluded: even a stolen
// CA key can't vouch for another host. The server certificate it signs renews itself.
// If the admin mounts their own certificate (custom.crt and custom.key), that is used
// instead. Keys never leave /data/tls.
package tlsca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"yardmaster/internal/fsutil"
)

const (
	caValidity     = 10 * 365 * 24 * time.Hour
	serverValidity = 397 * 24 * time.Hour // the most browsers accept
	renewBefore    = 30 * 24 * time.Hour
)

// Modes.
const (
	ModeBuiltin = "builtin"
	ModeCustom  = "custom"
)

type Manager struct {
	dir string

	mu     sync.RWMutex
	cert   *tls.Certificate
	caPEM  []byte
	caName string
}

// Open loads whatever certificates exist in dir.
func Open(dir string) (*Manager, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	os.Chmod(dir, 0o700)
	m := &Manager{dir: dir}
	if m.Mode() == ModeCustom {
		return m, m.loadCustom()
	}
	if err := m.loadCA(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if m.caPEM != nil {
		if err := m.loadServer(); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return m, nil
}

func (m *Manager) path(name string) string { return filepath.Join(m.dir, name) }

// Mode reports whether the admin mounted their own certificate.
func (m *Manager) Mode() string {
	if exists(m.path("custom.crt")) && exists(m.path("custom.key")) {
		return ModeCustom
	}
	return ModeBuiltin
}

// Ready reports whether there is a certificate to serve.
func (m *Manager) Ready() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cert != nil
}

// CAName is the name the built-in CA is limited to, or "".
func (m *Manager) CAName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.caName
}

// CAPEM returns the CA certificate for people to install (never the key).
func (m *Manager) CAPEM() []byte {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.caPEM
}

// Fingerprint is the SHA-256 fingerprint of the CA (or custom) certificate, to compare
// against what browsers show.
func (m *Manager) Fingerprint() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var der []byte
	if m.caPEM != nil {
		if b, _ := pem.Decode(m.caPEM); b != nil {
			der = b.Bytes
		}
	} else if m.cert != nil && len(m.cert.Certificate) > 0 {
		der = m.cert.Certificate[0]
	}
	if der == nil {
		return ""
	}
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = strings.ToUpper(hex.EncodeToString([]byte{b}))
	}
	return strings.Join(parts, ":")
}

// NotAfter is when the served certificate expires.
func (m *Manager) NotAfter() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cert == nil || m.cert.Leaf == nil {
		return time.Time{}
	}
	return m.cert.Leaf.NotAfter
}

// GetCertificate serves the current certificate, so renewals apply without a restart.
func (m *Manager) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cert == nil {
		return nil, errors.New("no certificate yet")
	}
	return m.cert, nil
}

// SetName makes sure the built-in CA and server certificate are for name. A new name
// means a new CA, because the CA is constrained to one name.
func (m *Manager) SetName(name string) (newCA bool, err error) {
	if m.Mode() == ModeCustom {
		return false, errors.New("a custom certificate is mounted; the name comes from it")
	}
	if err := ValidName(name); err != nil {
		return false, err
	}
	if m.CAName() != name {
		if err := m.createCA(name); err != nil {
			return false, err
		}
		newCA = true
	}
	return newCA, m.issueServer(name)
}

// RenewIfNeeded re-issues the server certificate when it's close to expiring, and
// reloads a custom certificate the admin replaced.
func (m *Manager) RenewIfNeeded() error {
	if m.Mode() == ModeCustom {
		return m.loadCustom()
	}
	name := m.CAName()
	if name == "" {
		return nil
	}
	if na := m.NotAfter(); na.IsZero() || time.Until(na) < renewBefore {
		return m.issueServer(name)
	}
	return nil
}

// ValidName accepts DNS names only. IP addresses get no certificate: DHCP can hand the
// address to another device, and the certificate would then vouch for it.
func ValidName(name string) error {
	if name == "" {
		return errors.New("enter the name people use to reach YardMaster")
	}
	if net.ParseIP(name) != nil {
		return errors.New("certificates are issued for names, not IP addresses; use a DNS name or bring your own certificate")
	}
	if len(name) > 253 {
		return errors.New("that name is too long")
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("%q isn't a valid host name", name)
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return fmt.Errorf("%q isn't a valid host name", name)
			}
		}
	}
	return nil
}

func (m *Manager) createCA(name string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	_, v4all, _ := net.ParseCIDR("0.0.0.0/0")
	_, v6all, _ := net.ParseCIDR("::/0")
	tpl := &x509.Certificate{
		SerialNumber:                serial,
		Subject:                     pkix.Name{CommonName: "YardMaster CA for " + name, Organization: []string{"YardMaster"}},
		NotBefore:                   time.Now().Add(-time.Hour),
		NotAfter:                    time.Now().Add(caValidity),
		KeyUsage:                    x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid:       true,
		IsCA:                        true,
		MaxPathLenZero:              true,
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{name},
		ExcludedIPRanges:            []*net.IPNet{v4all, v6all},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	if err := writeKey(m.path("ca.key"), key); err != nil {
		return err
	}
	if err := writePEM(m.path("ca.crt"), "CERTIFICATE", der, 0o644); err != nil {
		return err
	}
	return m.loadCA()
}

func (m *Manager) issueServer(name string) error {
	caCert, caKey, err := m.readCA()
	if err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(serverValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return err
	}
	if err := writeKey(m.path("server.key"), key); err != nil {
		return err
	}
	if err := writePEM(m.path("server.crt"), "CERTIFICATE", der, 0o644); err != nil {
		return err
	}
	return m.loadServer()
}

func (m *Manager) readCA() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPEM, err := os.ReadFile(m.path("ca.crt"))
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err := os.ReadFile(m.path("ca.key"))
	if err != nil {
		return nil, nil, err
	}
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return nil, nil, errors.New("the CA files in the tls folder are damaged")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, nil, err
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	return cert, key, err
}

func (m *Manager) loadCA() error {
	cert, _, err := m.readCA()
	if err != nil {
		return err
	}
	certPEM, _ := os.ReadFile(m.path("ca.crt"))
	name := ""
	if len(cert.PermittedDNSDomains) > 0 {
		name = cert.PermittedDNSDomains[0]
	}
	m.mu.Lock()
	m.caPEM, m.caName = certPEM, name
	m.mu.Unlock()
	return nil
}

func (m *Manager) loadServer() error {
	return m.loadPair(m.path("server.crt"), m.path("server.key"))
}

func (m *Manager) loadCustom() error {
	m.mu.Lock()
	m.caPEM, m.caName = nil, ""
	m.mu.Unlock()
	return m.loadPair(m.path("custom.crt"), m.path("custom.key"))
}

func (m *Manager) loadPair(certPath, keyPath string) error {
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.cert = &pair
	m.mu.Unlock()
	return nil
}

// CustomName is the first DNS name in a mounted certificate.
func (m *Manager) CustomName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cert == nil || m.cert.Leaf == nil {
		return ""
	}
	if len(m.cert.Leaf.DNSNames) > 0 {
		return m.cert.Leaf.DNSNames[0]
	}
	return m.cert.Leaf.Subject.CommonName
}

func writeKey(path string, key *ecdsa.PrivateKey) error {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	return writePEM(path, "EC PRIVATE KEY", der, 0o600)
}

func writePEM(path, kind string, der []byte, mode os.FileMode) error {
	return fsutil.WriteFileAtomic(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), mode)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
