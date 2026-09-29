package tlsca

import (
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func TestCAIsLimitedToItsName(t *testing.T) {
	m, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetName("192.168.1.10"); err == nil {
		t.Fatal("an IP address was accepted as a name")
	}
	if _, err := m.SetName("yardmaster.office.lan"); err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(m.CAPEM())
	ca, _ := x509.ParseCertificate(block.Bytes)
	if len(ca.PermittedDNSDomains) != 1 || ca.PermittedDNSDomains[0] != "yardmaster.office.lan" || len(ca.ExcludedIPRanges) != 2 {
		t.Fatalf("constraints %v %v", ca.PermittedDNSDomains, ca.ExcludedIPRanges)
	}
	cert, _ := m.GetCertificate(nil)
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	if _, err := cert.Leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "yardmaster.office.lan"}); err != nil {
		t.Fatalf("server certificate doesn't verify: %v", err)
	}
	// A reopened manager finds the same CA; a new name means a new CA.
	again, _ := Open(m.dir)
	if again.CAName() != "yardmaster.office.lan" || again.Fingerprint() != m.Fingerprint() {
		t.Fatal("CA not reloaded")
	}
	newCA, _ := again.SetName("other.office.lan")
	if !newCA || again.Fingerprint() == m.Fingerprint() {
		t.Fatal("renaming should create a new CA")
	}
}
