package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"yardmaster/internal/store"
)

func TestTokensFollowTheirOwner(t *testing.T) {
	ctx := context.Background()
	s := New(store.OpenTest(t))
	u, _, err := s.CreateUser(ctx, "alice", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	tok, value, err := s.CreateToken(ctx, u.ID, "laptop", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(value) < 40 || value[:3] != "ym_" {
		t.Fatalf("token %q should be long and start with ym_", value)
	}
	c, err := s.Authenticate(ctx, value)
	if err != nil || c.Username != "alice" || c.TokenName != "laptop" {
		t.Fatalf("authenticate: %+v, %v", c, err)
	}
	// Deactivating the owner stops the token without revoking it.
	if err := s.SetActive(ctx, u.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, value); err == nil {
		t.Fatal("token of a deactivated user still works")
	}
	s.SetActive(ctx, u.ID, true)
	if _, err := s.Authenticate(ctx, value); err != nil {
		t.Fatal("token should work again after reactivation")
	}
	// Revoked tokens stop working, and only the owner can revoke.
	other, _, _ := s.CreateUser(ctx, "bob", "")
	if _, err := s.RevokeToken(ctx, other.ID, tok.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another user revoked alice's token: %v", err)
	}
	if _, err := s.RevokeToken(ctx, u.ID, tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, value); err == nil {
		t.Fatal("revoked token still works")
	}
	if _, err := s.Authenticate(ctx, "ym_nonsense"); err == nil {
		t.Fatal("unknown token accepted")
	}
}

func TestTokenLimit(t *testing.T) {
	ctx := context.Background()
	s := New(store.OpenTest(t))
	u, _, _ := s.CreateUser(ctx, "alice", "")
	for i := 0; i < MaxTokensPerUser; i++ {
		if _, _, err := s.CreateToken(ctx, u.ID, "t", 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.CreateToken(ctx, u.ID, "one too many", 0); !errors.Is(err, ErrTooManyTokens) {
		t.Fatalf("want ErrTooManyTokens, got %v", err)
	}
}

func TestTemporaryPasswordFlow(t *testing.T) {
	ctx := context.Background()
	s := New(store.OpenTest(t))
	u, temp, err := s.CreateUser(ctx, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Login(ctx, "alice", temp, "1.2.3.4")
	if err != nil || !got.MustChangePassword {
		t.Fatalf("login with temporary password: %+v %v", got, err)
	}
	session, _ := s.NewSession(ctx, u.ID)
	if err := s.ChangePassword(ctx, u.ID, temp, "short", session); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("weak password accepted: %v", err)
	}
	if err := s.ChangePassword(ctx, u.ID, temp, "a much better one", session); err != nil {
		t.Fatal(err)
	}
	got, err = s.Login(ctx, "alice", "a much better one", "1.2.3.4")
	if err != nil || got.MustChangePassword {
		t.Fatalf("after change: %+v %v", got, err)
	}
	// A reset ends sessions and requires a new temporary password.
	newTemp, err := s.ResetPassword(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, session); err == nil {
		t.Fatal("session survived a password reset")
	}
	if _, err := s.Login(ctx, "alice", newTemp, "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	// An expired temporary password is refused.
	s.db.Exec(`UPDATE users SET temp_password_expires_at = ? WHERE id = ?`, time.Now().Add(-time.Hour).Unix(), u.ID)
	if _, err := s.Login(ctx, "alice", newTemp, "1.2.3.4"); !errors.Is(err, ErrTempExpired) {
		t.Fatalf("want ErrTempExpired, got %v", err)
	}
}

func TestLoginThrottle(t *testing.T) {
	ctx := context.Background()
	s := New(store.OpenTest(t))
	s.CreateAdmin(ctx, "admin", "correct horse battery")
	for i := 0; i < maxFailuresAccount; i++ {
		if _, err := s.Login(ctx, "admin", "wrong", "10.0.0.1"); !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, err := s.Login(ctx, "admin", "correct horse battery", "10.0.0.2"); !errors.Is(err, ErrThrottled) {
		t.Fatalf("account should be throttled even from another IP, got %v", err)
	}
}

func TestSessionIdleExpiry(t *testing.T) {
	ctx := context.Background()
	s := New(store.OpenTest(t))
	u, _ := s.CreateAdmin(ctx, "admin", "correct horse battery")
	tok, _ := s.NewSession(ctx, u.ID)
	if _, err := s.Session(ctx, tok); err != nil {
		t.Fatal(err)
	}
	s.db.Exec(`UPDATE sessions SET last_seen_at = ?`, time.Now().Add(-SessionIdle-time.Minute).Unix())
	if _, err := s.Session(ctx, tok); err == nil {
		t.Fatal("idle session still valid")
	}
}

func TestDeleteUserRemovesTokensAndSessions(t *testing.T) {
	ctx := context.Background()
	s := New(store.OpenTest(t))
	admin, _ := s.CreateAdmin(ctx, "admin", "correct horse battery")
	u, _, _ := s.CreateUser(ctx, "carol", "")
	_, value, _ := s.CreateToken(ctx, u.ID, "laptop", 0)
	session, _ := s.NewSession(ctx, u.ID)
	if _, err := s.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, value); err == nil {
		t.Fatal("token of a deleted user still works")
	}
	if _, err := s.Session(ctx, session); err == nil {
		t.Fatal("session of a deleted user still works")
	}
	if _, err := s.DeleteUser(ctx, admin.ID); err == nil {
		t.Fatal("the admin was deleted")
	}
}
