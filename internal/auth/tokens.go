package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Token is a gateway token as shown to its owner or the admin. The token value itself is
// only ever returned once, at creation.
type Token struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"user_id"`
	Name       string `json:"name"`
	Hint       string `json:"hint"`
	CreatedAt  int64  `json:"created_at"`
	ExpiresAt  *int64 `json:"expires_at,omitempty"`
	LastUsedAt *int64 `json:"last_used_at,omitempty"`
	RevokedAt  *int64 `json:"revoked_at,omitempty"`
}

// Caller is who a gateway request belongs to.
type Caller struct {
	UserID    int64
	Username  string
	TokenID   int64
	TokenName string
}

const tokenColumns = `id, user_id, name, hint, created_at, expires_at, last_used_at, revoked_at`

// CreateToken issues a new token for a user and returns its value, shown once.
// expiresInDays of 0 means no expiry.
func (s *Service) CreateToken(ctx context.Context, userID int64, name string, expiresInDays int) (*Token, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return nil, "", errors.New("give the token a name of up to 64 characters, like \"laptop\"")
	}
	var active int
	now := time.Now().Unix()
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM api_tokens WHERE user_id = ? AND revoked_at IS NULL
		 AND (expires_at IS NULL OR expires_at > ?)`, userID, now).Scan(&active); err != nil {
		return nil, "", err
	}
	if active >= MaxTokensPerUser {
		return nil, "", ErrTooManyTokens
	}
	value := TokenPrefix + randomString(32)
	var expires any
	if expiresInDays > 0 {
		expires = now + int64(expiresInDays)*86400
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO api_tokens (user_id, name, token_hash, hint, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, name, hashToken(value), value[:len(TokenPrefix)+6], now, expires)
	if err != nil {
		return nil, "", err
	}
	id, _ := res.LastInsertId()
	t, err := s.tokenByID(ctx, id)
	return t, value, err
}

// Tokens lists a user's tokens, newest first, including revoked and expired ones so the
// usage per token stays readable.
func (s *Service) Tokens(ctx context.Context, userID int64) ([]*Token, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+tokenColumns+` FROM api_tokens WHERE user_id = ? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Token{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeToken revokes one of a user's tokens.
func (s *Service) RevokeToken(ctx context.Context, userID, tokenID int64) (*Token, error) {
	t, err := s.tokenByID(ctx, tokenID)
	if err != nil {
		return nil, err
	}
	if t.UserID != userID {
		return nil, ErrNotFound
	}
	if t.RevokedAt == nil {
		now := time.Now().Unix()
		if _, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = ? WHERE id = ?`, now, tokenID); err != nil {
			return nil, err
		}
		t.RevokedAt = &now
	}
	return t, nil
}

// Authenticate checks a gateway token. The owner must be active and the token neither
// revoked nor expired.
func (s *Service) Authenticate(ctx context.Context, value string) (*Caller, error) {
	if !strings.HasPrefix(value, TokenPrefix) {
		return nil, ErrNotFound
	}
	var c Caller
	var expires, lastUsed sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT t.id, t.name, t.expires_at, t.last_used_at, u.id, u.username
		 FROM api_tokens t JOIN users u ON u.id = t.user_id
		 WHERE t.token_hash = ? AND t.revoked_at IS NULL AND u.active = 1`, hashToken(value)).
		Scan(&c.TokenID, &c.TokenName, &expires, &lastUsed, &c.UserID, &c.Username)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	if expires.Valid && now > expires.Int64 {
		return nil, ErrNotFound
	}
	// Record use at most once a minute per token.
	if !lastUsed.Valid || now-lastUsed.Int64 > 60 {
		s.db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, now, c.TokenID)
	}
	return &c, nil
}

func (s *Service) tokenByID(ctx context.Context, id int64) (*Token, error) {
	t, err := scanToken(s.db.QueryRowContext(ctx, `SELECT `+tokenColumns+` FROM api_tokens WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

func scanToken(row interface{ Scan(...any) error }) (*Token, error) {
	var t Token
	var expires, lastUsed, revoked sql.NullInt64
	if err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.Hint, &t.CreatedAt, &expires, &lastUsed, &revoked); err != nil {
		return nil, err
	}
	t.ExpiresAt = nullable(expires)
	t.LastUsedAt = nullable(lastUsed)
	t.RevokedAt = nullable(revoked)
	return &t, nil
}

func nullable(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}
