package secrets

import (
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
	if strings.Contains(string(raw), "sk-very-secret") || !strings.HasPrefix(string(raw), encryptedPrefix) {
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
	if !strings.HasPrefix(string(raw), encryptedPrefix) {
		t.Fatal("existing keys weren't encrypted when a master key was set")
	}
}
