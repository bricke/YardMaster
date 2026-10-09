package secrets

import (
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvWinsAndIsReadOnly(t *testing.T) {
	s, err := Open(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	s.lookup = func(name string) (string, bool) {
		if name == "FROM_ENV" {
			return "env-value", true
		}
		return "", false
	}
	if err := s.Set("FROM_ENV", "x"); err == nil {
		t.Fatal("setting a key the environment provides should be refused")
	}
	if err := s.Set("FROM_UI", "ui-value"); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.Status("FROM_UI"); st != FromUI {
		t.Fatalf("status %s", st)
	}
	if st, _ := s.Status("MISSING"); st != Missing {
		t.Fatalf("status %s", st)
	}
	env, _ := s.Env([]string{"FROM_UI", "FROM_ENV", "MISSING"})
	if strings.Join(env, ",") != "FROM_ENV=env-value,FROM_UI=ui-value" {
		t.Fatalf("env %v", env)
	}
}

func TestEncryptedAtRest(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, "a passphrase")
	s.Set("API_KEY", "sk-very-secret")
	raw, _ := os.ReadFile(filepath.Join(dir, "provider-keys"))
	if strings.Contains(string(raw), "sk-very-secret") || !strings.HasPrefix(string(raw), passphrasePrefix) {
		t.Fatalf("key stored in the clear: %s", raw)
	}
	info, _ := os.Stat(filepath.Join(dir, "provider-keys"))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v", info.Mode().Perm())
	}
	if _, err := Open(dir, "another passphrase"); err == nil {
		t.Fatal("opened with the wrong master key")
	}
	if _, err := Open(dir, ""); err == nil {
		t.Fatal("opened encrypted keys without a master key")
	}
	again, err := Open(dir, "a passphrase")
	if err != nil {
		t.Fatal(err)
	}
	env, _ := again.Env([]string{"API_KEY"})
	if len(env) != 1 || env[0] != "API_KEY=sk-very-secret" {
		t.Fatalf("env %v", env)
	}
}

func TestPlainFileGetsEncryptedWhenKeyAdded(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, "")
	s.Set("API_KEY", "sk-plain")
	if _, err := Open(dir, "now encrypted"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "provider-keys"))
	if !strings.HasPrefix(string(raw), passphrasePrefix) {
		t.Fatal("existing keys weren't encrypted when a master key was set")
	}
}

func TestPassphraseUsesArgon2idWithASalt(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, dir := range []string{a, b} {
		s, _ := Open(dir, "a passphrase")
		s.Set("API_KEY", "sk-1")
	}
	ra, _ := os.ReadFile(filepath.Join(a, "provider-keys"))
	rb, _ := os.ReadFile(filepath.Join(b, "provider-keys"))
	sa, _, okA := splitPassphraseFile(ra)
	sb, _, okB := splitPassphraseFile(rb)
	if !okA || !okB || string(sa) == string(sb) {
		t.Fatalf("want ymenc2 files with different salts: %q, %q", ra[:40], rb[:40])
	}
	// Saving again keeps the salt, so the file stays readable after a restart.
	s, _ := Open(a, "a passphrase")
	s.Set("OTHER_KEY", "sk-2")
	ra2, _ := os.ReadFile(filepath.Join(a, "provider-keys"))
	if sa2, _, _ := splitPassphraseFile(ra2); string(sa2) != string(sa) {
		t.Fatal("the salt changed on save")
	}
}

func TestSHA256FileIsReSavedWithArgon2id(t *testing.T) {
	dir := t.TempDir()
	// A file written before Argon2id: ymenc1, with a SHA-256 of the passphrase as the key.
	sum := sha256.Sum256([]byte("old passphrase"))
	aead, _ := newAEAD(sum[:])
	nonce := make([]byte, aead.NonceSize())
	sealed := aead.Seal(nonce, nonce, []byte(`{"API_KEY":"sk-old"}`), nil)
	os.WriteFile(filepath.Join(dir, "provider-keys"), []byte(keyPrefix+base64.StdEncoding.EncodeToString(sealed)), 0o600)

	if _, err := Open(dir, "wrong passphrase"); err == nil {
		t.Fatal("opened with the wrong passphrase")
	}
	s, err := Open(dir, "old passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if env, _ := s.Env([]string{"API_KEY"}); len(env) != 1 || env[0] != "API_KEY=sk-old" {
		t.Fatalf("env %v", env)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "provider-keys"))
	if !strings.HasPrefix(string(raw), passphrasePrefix) {
		t.Fatalf("not re-saved as ymenc2: %q", raw[:10])
	}
	again, err := Open(dir, "old passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if env, _ := again.Env([]string{"API_KEY"}); len(env) != 1 {
		t.Fatalf("after the re-save: %v", env)
	}
}

func TestRawKeyKeepsItsFormat(t *testing.T) {
	dir := t.TempDir()
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	s, _ := Open(dir, key)
	s.Set("API_KEY", "sk-raw")
	raw, _ := os.ReadFile(filepath.Join(dir, "provider-keys"))
	if !strings.HasPrefix(string(raw), keyPrefix) {
		t.Fatalf("a 32-byte key should write ymenc1: %q", raw[:10])
	}
	if _, err := Open(dir, key); err != nil {
		t.Fatal(err)
	}
	// A passphrase file can't be read with a raw key, even one that happens to decode.
	pdir := t.TempDir()
	p, _ := Open(pdir, "a passphrase")
	p.Set("API_KEY", "sk-1")
	if _, err := Open(pdir, key); err == nil || !strings.Contains(err.Error(), "doesn't match") {
		t.Fatalf("raw key on a passphrase file: %v", err)
	}
}

func TestYardMasterVariablesCantHoldKeys(t *testing.T) {
	s, err := Open(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	s.lookup = func(name string) (string, bool) {
		if name == "YARDMASTER_SECRET_KEY" {
			return "the-master-key", true
		}
		return "", false
	}
	if err := s.Set("YARDMASTER_PROXY_SECRET", "x"); err == nil {
		t.Error("a key was stored under a YARDMASTER_ name")
	}
	// The status doesn't tell whether the variable is set.
	if st, _ := s.Status("YARDMASTER_SECRET_KEY"); st != Missing {
		t.Errorf("status %s, want %s", st, Missing)
	}
	if env, _ := s.Env([]string{"YARDMASTER_SECRET_KEY"}); len(env) != 0 {
		t.Errorf("YardMaster's own secret reached the env: %v", env)
	}
}
