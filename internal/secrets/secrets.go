// Package secrets stores provider API keys set from the UI.
//
// A key named in the container environment always wins and is read-only in the UI. Keys
// set from the UI live in one file readable only by the container user (0600). When
// YARDMASTER_SECRET_KEY is set, that file is encrypted with AES-GCM, so a copy of /data
// alone reveals nothing. Key values are never returned by the API or logged.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"

	"yardmaster/internal/fsutil"
)

// Status of one key, as the UI shows it.
const (
	FromEnv = "env"
	FromUI  = "ui"
	Missing = "missing"
)

const encryptedPrefix = "ymenc1:"

var envNamePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)

// ValidName reports whether name can be used as an environment variable name.
func ValidName(name string) bool { return envNamePattern.MatchString(name) }

// Store holds UI-set keys.
type Store struct {
	mu     sync.Mutex
	path   string
	aead   cipher.AEAD // nil when no master key is set
	lookup func(string) (string, bool)
}

// Open prepares the store in dir. masterKey may be empty.
func Open(dir, masterKey string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "provider-keys"), lookup: os.LookupEnv}
	if masterKey != "" {
		// A base64-encoded 32-byte value is used as is; anything else (a passphrase) is
		// stretched with SHA-256.
		key, err := base64.StdEncoding.DecodeString(masterKey)
		if err != nil || len(key) != 32 {
			sum := sha256.Sum256([]byte(masterKey))
			key = sum[:]
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		if s.aead, err = cipher.NewGCM(block); err != nil {
			return nil, err
		}
	}
	// Fail at start, not at the first apply, if the file can't be read with this key.
	if _, err := s.load(); err != nil {
		return nil, err
	}
	// Re-save so an existing plain file becomes encrypted once a master key is set.
	if s.aead != nil {
		keys, _ := s.load()
		if len(keys) > 0 {
			if err := s.save(keys); err != nil {
				return nil, err
			}
		}
	}
	return s, nil
}

// Encrypted reports whether keys are encrypted at rest.
func (s *Store) Encrypted() bool { return s.aead != nil }

// Status reports where the value for an environment variable name comes from.
func (s *Store) Status(name string) (string, error) {
	if v, ok := s.lookup(name); ok && v != "" {
		return FromEnv, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	keys, err := s.load()
	if err != nil {
		return "", err
	}
	if keys[name] != "" {
		return FromUI, nil
	}
	return Missing, nil
}

// Set stores a key. It refuses names already set in the container environment, since
// that value takes precedence and the UI treats it as read-only.
func (s *Store) Set(name, value string) error {
	if !ValidName(name) {
		return fmt.Errorf("%q isn't a valid environment variable name", name)
	}
	if value == "" {
		return errors.New("the key is empty")
	}
	if v, ok := s.lookup(name); ok && v != "" {
		return fmt.Errorf("%s is set in the container environment, which takes precedence; change it there", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	keys, err := s.load()
	if err != nil {
		return err
	}
	keys[name] = value
	return s.save(keys)
}

// Delete removes a UI-set key.
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys, err := s.load()
	if err != nil {
		return err
	}
	delete(keys, name)
	return s.save(keys)
}

// Env returns NAME=value pairs for the given names, for switchyard-server's environment.
// Container environment values win over UI-set ones. Missing names are left out.
func (s *Store) Env(names []string) ([]string, error) {
	s.mu.Lock()
	keys, err := s.load()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var out []string
	for _, name := range names {
		if v, ok := s.lookup(name); ok && v != "" {
			out = append(out, name+"="+v)
		} else if v := keys[name]; v != "" {
			out = append(out, name+"="+v)
		}
	}
	return out, nil
}

func (s *Store) load() (map[string]string, error) {
	keys := map[string]string{}
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return keys, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) >= len(encryptedPrefix) && string(raw[:len(encryptedPrefix)]) == encryptedPrefix {
		if s.aead == nil {
			return nil, errors.New("provider keys are encrypted but YARDMASTER_SECRET_KEY is not set")
		}
		data, err := base64.StdEncoding.DecodeString(string(raw[len(encryptedPrefix):]))
		if err != nil || len(data) < s.aead.NonceSize() {
			return nil, errors.New("provider keys file is damaged")
		}
		nonce, sealed := data[:s.aead.NonceSize()], data[s.aead.NonceSize():]
		if raw, err = s.aead.Open(nil, nonce, sealed, nil); err != nil {
			return nil, errors.New("can't decrypt provider keys: YARDMASTER_SECRET_KEY doesn't match the one they were saved with")
		}
	}
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, errors.New("provider keys file is damaged")
	}
	return keys, nil
}

func (s *Store) save(keys map[string]string) error {
	data, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	if s.aead != nil {
		nonce := make([]byte, s.aead.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return err
		}
		sealed := s.aead.Seal(nonce, nonce, data, nil)
		data = []byte(encryptedPrefix + base64.StdEncoding.EncodeToString(sealed))
	}
	return fsutil.WriteFileAtomic(s.path, data, 0o600)
}
