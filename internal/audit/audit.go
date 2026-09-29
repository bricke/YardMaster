// Package audit records security-relevant actions.
//
// Entries name what changed, never secret values: a key's name, not the key.
package audit

import (
	"context"
	"log/slog"

	"yardmaster/internal/store"
)

// Actions recorded in the log. Keeping them in one list makes the audit page's
// filter and this package agree.
const (
	LoginOK            = "login"
	LoginFailed        = "login.failed"
	Logout             = "logout"
	PasswordChanged    = "password.changed"
	PasswordReset      = "password.reset"
	UserCreated        = "user.created"
	UserDeactivated    = "user.deactivated"
	UserReactivated    = "user.reactivated"
	UserDeleted        = "user.deleted"
	TokenCreated       = "token.created"
	TokenRevoked       = "token.revoked"
	KeySet             = "key.set"
	KeyDeleted         = "key.deleted"
	ConfigApplied      = "config.applied"
	ConfigRolledBack   = "config.rolled_back"
	SwitchyardRestart  = "switchyard.restarted"
	StatsReset         = "switchyard.stats_reset"
	PriceSet           = "price.set"
	PriceDeleted       = "price.deleted"
	HTTPSChanged       = "https.changed"
	AdminCreated       = "admin.created"
	AdminPasswordReset = "admin.password_reset_cli"
)

// Log writes audit entries.
type Log struct {
	db *store.DB
}

func New(db *store.DB) *Log { return &Log{db: db} }

// Record writes one entry. A failure to write is logged but never fails the action
// itself: the action already happened.
func (l *Log) Record(ctx context.Context, actor, ip, action, detail string) {
	_, err := l.db.ExecContext(ctx,
		`INSERT INTO audit_log (created_at, actor, ip, action, detail) VALUES (?, ?, ?, ?, ?)`,
		store.Now(), actor, ip, action, detail)
	if err != nil {
		slog.Error("audit log write failed", "action", action, "err", err)
	}
}

// Entry is one row of the audit log.
type Entry struct {
	ID        int64  `json:"id"`
	CreatedAt int64  `json:"created_at"`
	Actor     string `json:"actor"`
	IP        string `json:"ip"`
	Action    string `json:"action"`
	Detail    string `json:"detail"`
}

// Query returns entries newest first, optionally filtered by actor or action prefix.
func (l *Log) Query(ctx context.Context, actor, action string, before int64, limit int) ([]Entry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, created_at, actor, ip, action, detail FROM audit_log WHERE 1=1`
	var args []any
	if actor != "" {
		q += ` AND actor = ?`
		args = append(args, actor)
	}
	if action != "" {
		q += ` AND action LIKE ?`
		args = append(args, action+"%")
	}
	if before > 0 {
		q += ` AND id < ?`
		args = append(args, before)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := l.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.CreatedAt, &e.Actor, &e.IP, &e.Action, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Prune deletes entries older than the retention period.
func (l *Log) Prune(ctx context.Context, days int) (int64, error) {
	res, err := l.db.ExecContext(ctx, `DELETE FROM audit_log WHERE created_at < ?`, store.Now()-int64(days)*86400)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
