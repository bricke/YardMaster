// Package auth holds every credential YardMaster knows about: user accounts and their
// passwords, browser sessions, gateway tokens and login throttling.
//
// Sessions and tokens are random 256-bit values stored only as SHA-256 hashes, so a copy
// of the database gives none of them away. Passwords are bcrypt hashes.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"yardmaster/internal/store"
)

const (
	RoleAdmin = "admin"
	RoleUser  = "user"

	// Where an account comes from: YardMaster's own sign-in, or another proxy.
	SourceBuiltin = "builtin"
	SourceProxy   = "proxy"

	SessionLifetime  = 24 * time.Hour
	SessionIdle      = 2 * time.Hour
	TempPasswordTTL  = 7 * 24 * time.Hour
	MaxTokensPerUser = 25
	TokenPrefix      = "ym_"
	MinPasswordLen   = 10
	bcryptCost       = 12
)

var (
	ErrBadCredentials = errors.New("wrong username or password")
	ErrThrottled      = errors.New("too many failed logins, try again later")
	ErrBusy           = errors.New("too many sign-ins at once from your network, try again in a moment")
	ErrInactive       = errors.New("this account is deactivated")
	ErrTempExpired    = errors.New("the temporary password has expired, ask the admin for a new one")
	ErrNotFound       = errors.New("not found")
	ErrTooManyTokens  = fmt.Errorf("a user can have at most %d active tokens", MaxTokensPerUser)
	ErrUsernameTaken  = errors.New("that username is already taken")
	ErrWeakPassword   = fmt.Errorf("passwords need at least %d characters", MinPasswordLen)
	usernamePattern   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)
)

// A bcrypt hash of a random value, compared against when a username doesn't exist, so an
// unknown username takes as long to reject as a wrong password.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte(randomString(24)), bcryptCost)

// User is an account. Admin and user roles only.
type User struct {
	ID                 int64  `json:"id"`
	Username           string `json:"username"`
	DisplayName        string `json:"display_name"`
	Role               string `json:"role"`
	MustChangePassword bool   `json:"must_change_password"`
	TempExpiresAt      *int64 `json:"temp_password_expires_at,omitempty"`
	Active             bool   `json:"active"`
	Source             string `json:"source"`
	CreatedAt          int64  `json:"created_at"`
	passwordHash       string
}

// Service is the entry point for all credential operations.
type Service struct {
	db       *store.DB
	throttle *Throttle
}

func New(db *store.DB) *Service {
	return &Service{db: db, throttle: NewThrottle()}
}

const userColumns = `id, username, display_name, role, password_hash, must_change_password,
	temp_password_expires_at, active, source, created_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var temp sql.NullInt64
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &u.passwordHash,
		&u.MustChangePassword, &temp, &u.Active, &u.Source, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.TempExpiresAt = nullable(temp)
	return &u, nil
}

// UserByID returns one user.
func (s *Service) UserByID(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id))
}

// UserByName returns one user by username (case-insensitive).
func (s *Service) UserByName(ctx context.Context, username string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE username = ?`, username))
}

// Users lists every account, admins first.
func (s *Service) Users(ctx context.Context) ([]*User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY role = 'user', username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// HasAdmin reports whether an admin account exists.
func (s *Service) HasAdmin(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = ? AND source = ?`, RoleAdmin, SourceBuiltin).Scan(&n)
	return n > 0, err
}

// ValidUsername reports whether name can be used as a username. Usernames also label
// requests for Switchyard (x-switchyard-origin), so they stay short and plain.
func ValidUsername(name string) bool { return usernamePattern.MatchString(name) }

// CreateAdmin creates the one admin account with a password of their choosing.
func (s *Service) CreateAdmin(ctx context.Context, username, password string) (*User, error) {
	if err := checkPassword(password); err != nil {
		return nil, err
	}
	return s.insertUser(ctx, username, "Administrator", RoleAdmin, password, false)
}

// CreateUser creates a user with a generated temporary password, returned once for the
// admin to hand over.
func (s *Service) CreateUser(ctx context.Context, username, displayName string) (*User, string, error) {
	temp := TempPassword()
	u, err := s.insertUser(ctx, username, displayName, RoleUser, temp, true)
	return u, temp, err
}

func (s *Service) insertUser(ctx context.Context, username, displayName, role, password string, temporary bool) (*User, error) {
	username = strings.TrimSpace(username)
	if !ValidUsername(username) {
		return nil, errors.New("usernames use letters, digits, dots, dashes and underscores (up to 64)")
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = username
	}
	hash, tempExpires, err := hashPassword(password, temporary)
	if err != nil {
		return nil, err
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (username, display_name, role, password_hash, must_change_password,
		  temp_password_expires_at, active, source, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?)`,
		username, displayName, role, hash, temporary, tempExpires, SourceBuiltin, store.Now())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrUsernameTaken
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.UserByID(ctx, id)
}

// ProxyUser returns the account for a user named by another proxy, creating it on first
// sight, and refuses it if an admin deactivated it. Its role follows the proxy's role
// header on every request.
//
// A name that belongs to a built-in account (one from before YardMaster moved behind the
// proxy) is taken to be the same person, but that account's stored role is left alone:
// the proxy's role applies to the request only. Going back to built-in sign-in then
// neither keeps someone the proxy promoted an admin nor leaves the admin demoted.
func (s *Service) ProxyUser(ctx context.Context, username, role string) (*User, error) {
	u, err := s.UserByName(ctx, username)
	if errors.Is(err, ErrNotFound) {
		if !ValidUsername(username) {
			return nil, errors.New("the proxy sent an unusable user name")
		}
		// Two first requests can race to create the account; both then read the same row.
		_, err = s.db.ExecContext(ctx,
			`INSERT INTO users (username, display_name, role, active, source, created_at)
			 VALUES (?, ?, ?, 1, ?, ?) ON CONFLICT(username) DO NOTHING`,
			username, username, role, SourceProxy, store.Now())
		if err != nil {
			return nil, err
		}
		u, err = s.UserByName(ctx, username)
	}
	if err != nil {
		return nil, err
	}
	if !u.Active {
		return nil, ErrInactive
	}
	if u.Role != role && u.Source == SourceProxy {
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET role = ? WHERE id = ?`, role, u.ID); err != nil {
			return nil, err
		}
	}
	u.Role = role
	return u, nil
}

// Login checks a username and password. The caller's IP is used for throttling.
func (s *Service) Login(ctx context.Context, username, password, ip string) (*User, error) {
	if err := s.throttle.Begin(username, ip); err != nil {
		return nil, err
	}
	defer s.throttle.Done(username, ip)
	u, err := s.UserByName(ctx, username)
	if errors.Is(err, ErrNotFound) || (err == nil && u.Source != SourceBuiltin) {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		s.throttle.Fail(username, ip)
		return nil, ErrBadCredentials
	}
	if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.passwordHash), []byte(password)) != nil {
		s.throttle.Fail(username, ip)
		return nil, ErrBadCredentials
	}
	if !u.Active {
		return nil, ErrInactive
	}
	if u.MustChangePassword && u.TempExpiresAt != nil && time.Now().Unix() > *u.TempExpiresAt {
		return nil, ErrTempExpired
	}
	s.throttle.Reset(username)
	return u, nil
}

// ChangePassword sets a user's own password and ends their other sessions.
func (s *Service) ChangePassword(ctx context.Context, userID int64, current, next string, keepSession string) error {
	u, err := s.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.passwordHash), []byte(current)) != nil {
		return ErrBadCredentials
	}
	if err := checkPassword(next); err != nil {
		return err
	}
	if next == current {
		return errors.New("choose a password different from the current one")
	}
	if err := s.setPassword(ctx, userID, next, false); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND token_hash != ?`, userID, hashToken(keepSession))
	return err
}

// ResetPassword gives a user a new temporary password. Their sessions end; their
// gateway tokens keep working so their agents aren't interrupted.
func (s *Service) ResetPassword(ctx context.Context, userID int64) (string, error) {
	temp := TempPassword()
	if err := s.setPassword(ctx, userID, temp, true); err != nil {
		return "", err
	}
	return temp, s.endSessions(ctx, userID)
}

// SetAdminPassword replaces the admin's password; used by `yardmaster reset-password`.
func (s *Service) SetAdminPassword(ctx context.Context, password string) (*User, error) {
	if err := checkPassword(password); err != nil {
		return nil, err
	}
	u, err := scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE role = ? AND source = ? ORDER BY id LIMIT 1`, RoleAdmin, SourceBuiltin))
	if err != nil {
		return nil, err
	}
	if err := s.setPassword(ctx, u.ID, password, false); err != nil {
		return nil, err
	}
	s.throttle.Reset(u.Username)
	return u, s.endSessions(ctx, u.ID)
}

func (s *Service) setPassword(ctx context.Context, userID int64, password string, temporary bool) error {
	hash, tempExpires, err := hashPassword(password, temporary)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, must_change_password = ?, temp_password_expires_at = ? WHERE id = ?`,
		hash, temporary, tempExpires, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetActive activates or deactivates a user. Deactivating ends their sessions at once,
// and their tokens stop working because every token check also checks the owner.
func (s *Service) SetActive(ctx context.Context, userID int64, active bool) error {
	u, err := s.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.Role == RoleAdmin && !active {
		return errors.New("the admin account can't be deactivated")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET active = ? WHERE id = ?`, active, userID); err != nil {
		return err
	}
	if !active {
		return s.endSessions(ctx, userID)
	}
	return nil
}

// DeleteUser removes an account with its sessions and tokens (foreign keys cascade). Usage
// rows keep a snapshot of the name, so totals stay correct; the caller moves them to a
// label of their own (usage.Ledger.RelabelUser). The admin can't be deleted.
func (s *Service) DeleteUser(ctx context.Context, userID int64) (*User, error) {
	u, err := s.UserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u.Role == RoleAdmin {
		return nil, errors.New("the admin account can't be deleted")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID); err != nil {
		return nil, err
	}
	return u, nil
}

func checkPassword(password string) error {
	if len(password) < MinPasswordLen {
		return ErrWeakPassword
	}
	return nil
}

// hashPassword returns the bcrypt hash to store and, for a temporary password, when it
// expires (nil otherwise).
func hashPassword(password string, temporary bool) (hash string, tempExpires any, err error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", nil, err
	}
	if temporary {
		tempExpires = time.Now().Add(TempPasswordTTL).Unix()
	}
	return string(b), tempExpires, nil
}

// ---- sessions ----

// NewSession creates a session and returns the value for the cookie.
func (s *Service) NewSession(ctx context.Context, userID int64) (string, error) {
	token := randomString(32)
	now := time.Now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, created_at, last_seen_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		hashToken(token), userID, now.Unix(), now.Unix(), now.Add(SessionLifetime).Unix())
	return token, err
}

// Session returns the user behind a session cookie value, if the session is still valid.
func (s *Service) Session(ctx context.Context, token string) (*User, error) {
	if token == "" {
		return nil, ErrNotFound
	}
	var id, userID, lastSeen, expires int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, last_seen_at, expires_at FROM sessions WHERE token_hash = ?`, hashToken(token)).
		Scan(&id, &userID, &lastSeen, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	if now > expires || now-lastSeen > int64(SessionIdle.Seconds()) {
		s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
		return nil, ErrNotFound
	}
	u, err := s.UserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !u.Active {
		return nil, ErrNotFound
	}
	// Refresh the idle clock at most once a minute to keep writes down.
	if now-lastSeen > 60 {
		s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE id = ?`, now, id)
	}
	return u, nil
}

// endSessions signs a user out everywhere.
func (s *Service) endSessions(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// EndSession deletes one session (logout).
func (s *Service) EndSession(ctx context.Context, token string) {
	s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
}

// PruneSessions deletes expired sessions.
func (s *Service) PruneSessions(ctx context.Context) {
	now := time.Now().Unix()
	s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ? OR last_seen_at < ?`,
		now, now-int64(SessionIdle.Seconds()))
}

// ---- helpers ----

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// randomString returns n random bytes, base64url-encoded without padding.
func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// TempPassword returns a 16-character password without look-alike characters, easy to
// read out or type from a note.
func TempPassword() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}
