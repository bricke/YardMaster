// Package secrets stores provider API keys set from the UI.
//
// A key named in the container environment always wins and is read-only in the UI. Keys
// set from the UI live in one file readable only by the container user (0600). When
// YARDMASTER_SECRET_KEY is set, that file is encrypted with AES-GCM, so a copy of /data
// alone reveals nothing. Key values are never returned by the API or logged.
//
// The master key is either a base64-encoded 32-byte key, used as is, or a passphrase,
// stretched with Argon2id and a random salt kept in the file:
//
//	ymenc1:<base64 nonce+ciphertext>                 a 32-byte key
//	ymenc2:<base64 salt>:<base64 nonce+ciphertext>   a passphrase
//
// Files written before passphrases used Argon2id are ymenc1 with a SHA-256 of the
// passphrase as the key; they're read and re-saved as ymenc2 at start.
package secrets

import (
	"bytes"
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
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"

	"yardmaster/internal/fsutil"
)

// Status of one key, as the UI shows it.
const (
	FromEnv = "env"
	FromUI  = "ui"
	Missing = "missing"
)

const (
	keyPrefix        = "ymenc1:"
	passphrasePrefix = "ymenc2:"
	saltSize         = 16
)

// Argon2id parameters: RFC 9106's second recommended option. The key is derived once, at
// start.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
)

var envNamePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)

// reservedPrefix names YardMaster's own settings, among them its secrets (master key,
// proxy secret, admin password). No provider key can be read from one.
const reservedPrefix = "YARDMASTER_"

// ValidName reports whether name can hold a provider key: an environment variable name
// outside YARDMASTER_*. A config naming one of YardMaster's own variables would otherwise
// hand its value to switchyard-server, which sends it to the provider's base URL.
func ValidName(name string) bool {
	return envNamePattern.MatchString(name) && !strings.HasPrefix(name, reservedPrefix)
}

// Store holds UI-set keys.
type Store struct {
	mu     sync.Mutex
	path   string
	aead   cipher.AEAD // nil when no master key is set
	lookup func(string) (string, bool)
	// With a passphrase: the salt of the Argon2id key in aead, and legacy, the SHA-256 key
	// that reads files from before Argon2id.
	salt   []byte
	legacy cipher.AEAD
}

// Open prepares the store in dir. masterKey may be empty.
func Open(dir, masterKey string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "provider-keys"), lookup: os.LookupEnv}
	if masterKey != "" {
		if err := s.setMasterKey(masterKey); err != nil {
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

// setMasterKey prepares the cipher for a raw key or a passphrase. A passphrase keeps the
// salt already in the file, so the file stays readable, or gets a new one.
func (s *Store) setMasterKey(masterKey string) error {
	if key, err := base64.StdEncoding.DecodeString(masterKey); err == nil && len(key) == 32 {
		var err error
		s.aead, err = newAEAD(key)
		return err
	}
	sum := sha256.Sum256([]byte(masterKey))
	legacy, err := newAEAD(sum[:])
	if err != nil {
		return err
	}
	s.legacy = legacy
	raw, err := os.ReadFile(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if salt, _, ok := splitPassphraseFile(raw); ok {
		s.salt = salt
	} else {
		s.salt = make([]byte, saltSize)
		if _, err := rand.Read(s.salt); err != nil {
			return err
		}
	}
	s.aead, err = newAEAD(argon2.IDKey([]byte(masterKey), s.salt, argonTime, argonMemory, argonThreads, 32))
	return err
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// splitPassphraseFile splits an ymenc2 file into its salt and sealed data.
func splitPassphraseFile(raw []byte) (salt, sealed []byte, ok bool) {
	rest, found := strings.CutPrefix(string(raw), passphrasePrefix)
	if !found {
		return nil, nil, false
	}
	saltText, sealedText, found := strings.Cut(rest, ":")
	if !found {
		return nil, nil, false
	}
	salt, err := base64.StdEncoding.DecodeString(saltText)
	if err != nil || len(salt) != saltSize {
		return nil, nil, false
	}
	sealed, err = base64.StdEncoding.DecodeString(sealedText)
	if err != nil {
		return nil, nil, false
	}
	return salt, sealed, true
}

// Encrypted reports whether keys are encrypted at rest.
func (s *Store) Encrypted() bool { return s.aead != nil }

// Status reports where the value for an environment variable name comes from. An invalid
// name is always missing, so the status never tells whether a reserved variable is set.
func (s *Store) Status(name string) (string, error) {
	if !ValidName(name) {
		return Missing, nil
	}
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
		return fmt.Errorf("%q can't hold a provider key: use a variable name like OPENROUTER_API_KEY, outside YARDMASTER_*", name)
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
// Container environment values win over UI-set ones. Missing and invalid names are left
// out.
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
		if !ValidName(name) {
			continue
		}
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
	var aead cipher.AEAD
	var data []byte
	switch {
	case strings.HasPrefix(string(raw), passphrasePrefix):
		salt, sealed, ok := splitPassphraseFile(raw)
		if !ok {
			return nil, errors.New("provider keys file is damaged")
		}
		// Only the passphrase's Argon2id key reads it; a raw key can't.
		if s.salt != nil && bytes.Equal(salt, s.salt) {
			aead = s.aead
		}
		data = sealed
	case strings.HasPrefix(string(raw), keyPrefix):
		aead = s.aead
		if s.legacy != nil {
			aead = s.legacy
		}
		var err error
		if data, err = base64.StdEncoding.DecodeString(string(raw[len(keyPrefix):])); err != nil {
			return nil, errors.New("provider keys file is damaged")
		}
	default:
		if err := json.Unmarshal(raw, &keys); err != nil {
			return nil, errors.New("provider keys file is damaged")
		}
		return keys, nil
	}
	if s.aead == nil {
		return nil, errors.New("provider keys are encrypted but YARDMASTER_SECRET_KEY is not set")
	}
	errMismatch := errors.New("can't decrypt provider keys: YARDMASTER_SECRET_KEY doesn't match the one they were saved with")
	if aead == nil {
		return nil, errMismatch
	}
	if len(data) < aead.NonceSize() {
		return nil, errors.New("provider keys file is damaged")
	}
	raw, err = aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], nil)
	if err != nil {
		return nil, errMismatch
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
		sealed := base64.StdEncoding.EncodeToString(s.aead.Seal(nonce, nonce, data, nil))
		if s.salt != nil {
			data = []byte(passphrasePrefix + base64.StdEncoding.EncodeToString(s.salt) + ":" + sealed)
		} else {
			data = []byte(keyPrefix + sealed)
		}
	}
	return fsutil.WriteFileAtomic(s.path, data, 0o600)
}
