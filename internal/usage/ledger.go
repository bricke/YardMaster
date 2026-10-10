// Package usage keeps the usage ledger: one row per request, built from
// two sources that share a request ID:
//
//   - the gateway, which knows who sent the request, how long it took and its status;
//   - Switchyard's routing log, which knows the served model and the tokens.
//
// Each source fills in its own columns, in whichever order they arrive. Rows are written
// by one background goroutine, so accounting never slows down or breaks a response.
package usage

import (
	"context"
	"log/slog"
	"time"

	"yardmaster/internal/deploy"
	"yardmaster/internal/store"
)

// GatewayEvent is what the gateway knows about a request.
type GatewayEvent struct {
	RequestID string
	At        time.Time
	UserID    int64
	UserName  string
	TokenID   int64
	TokenName string
	Route     string
	Status    int
	LatencyMS int64
	Error     string
}

// Record is one line of Switchyard's routing log (--routing-log-file). Field names follow
// switchyard-server v0.3.0's routing_log.rs.
type Record struct {
	TS                  string `json:"ts"`
	RouteID             string `json:"route_id"`
	Algorithm           string `json:"algorithm"`
	Origin              string `json:"origin"`
	TrialID             string `json:"trial_id"`
	SessionID           string `json:"session_id"`
	Model               string `json:"model"`
	Tier                string `json:"tier"`
	PromptTokens        int64  `json:"prompt_tokens"`
	CachedTokens        int64  `json:"cached_tokens"`
	CacheCreationTokens int64  `json:"cache_creation_tokens"`
	CompletionTokens    int64  `json:"completion_tokens"`
	ReasoningTokens     int64  `json:"reasoning_tokens"`
}

// classifierTier is the tier switchyard-server writes for classifier and judge calls
// made while routing. Their tokens count toward the request but aren't the answer.
const classifierTier = "classifier"

// Ledger writes usage rows.
type Ledger struct {
	db     *store.DB
	prices *Prices
	config func() *deploy.Parsed
	queue  chan func(context.Context)
	// Users deleted recently, by ID, so requests they started before the deletion and
	// that finish after it are filed under their label too. Only the writer touches it.
	deleted map[int64]deletion
}

type deletion struct {
	label string
	at    time.Time
}

// keepDeleted is how long a deletion is remembered: longer than any request runs.
const keepDeleted = time.Hour

func NewLedger(db *store.DB, prices *Prices, config func() *deploy.Parsed) *Ledger {
	return &Ledger{db: db, prices: prices, config: config, queue: make(chan func(context.Context), 4096),
		deleted: map[int64]deletion{}}
}

// Run writes queued rows until ctx ends, then drains what's left.
func (l *Ledger) Run(ctx context.Context) {
	for {
		select {
		case job := <-l.queue:
			job(context.WithoutCancel(ctx))
		case <-ctx.Done():
			for {
				select {
				case job := <-l.queue:
					job(context.WithoutCancel(ctx))
				default:
					return
				}
			}
		}
	}
}

func (l *Ledger) enqueue(job func(context.Context)) {
	select {
	case l.queue <- job:
	default:
		// The disk can't keep up. Dropping a usage row is better than stalling a
		// response; say so loudly.
		slog.Error("usage ledger queue full, dropping a usage row")
	}
}

// AddGateway queues the gateway's side of a request.
func (l *Ledger) AddGateway(e GatewayEvent) {
	l.enqueue(func(ctx context.Context) {
		var userID, tokenID any
		if e.UserID != 0 {
			userID = e.UserID
		}
		if e.TokenID != 0 {
			tokenID = e.TokenID
		}
		// A request started before its user was deleted is theirs, not a later user's who
		// was given the same name or ID.
		if d, ok := l.deleted[e.UserID]; ok && e.At.Before(d.at) {
			e.UserName = d.label
		}
		_, err := l.db.ExecContext(ctx,
			`INSERT INTO usage_events (request_id, created_at, user_id, user_name, token_id, token_name,
			   route, status, latency_ms, error)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(request_id) DO UPDATE SET
			   created_at = excluded.created_at, user_id = excluded.user_id, user_name = excluded.user_name,
			   token_id = excluded.token_id, token_name = excluded.token_name,
			   route = CASE WHEN excluded.route != '' THEN excluded.route ELSE usage_events.route END,
			   status = excluded.status, latency_ms = excluded.latency_ms, error = excluded.error`,
			e.RequestID, e.At.Unix(), userID, e.UserName, tokenID, e.TokenName,
			e.Route, e.Status, e.LatencyMS, e.Error)
		if err != nil {
			slog.Error("writing usage row", "err", err)
		}
	})
}

// AddRecord queues one routing-log record. requestID is the record's trial ID when the
// gateway set one, or a stable ID derived from the record's position in the log.
func (l *Ledger) AddRecord(requestID string, r Record) {
	l.enqueue(func(ctx context.Context) { l.writeRecord(ctx, requestID, r) })
}

// addCost ends the upsert of a routing record: it adds the record's cost to its request's
// row. A record without a price marks the row's cost unknown, so it never looks free.
const addCost = `
	cost = CASE WHEN excluded.cost IS NULL THEN usage_events.cost
	            ELSE COALESCE(usage_events.cost, 0) + excluded.cost END,
	cost_unknown = usage_events.cost_unknown OR excluded.cost_unknown,
	currency = CASE WHEN excluded.currency != '' THEN excluded.currency ELSE usage_events.currency END`

func (l *Ledger) writeRecord(ctx context.Context, requestID string, r Record) {
	at := time.Now()
	if t, err := time.Parse(time.RFC3339Nano, r.TS); err == nil {
		at = t
	}
	target, tier := l.config().Resolve(r.RouteID, r.Model)
	cost, known, currency := l.prices.Cost(target, r)
	var costV any
	if known {
		costV = cost
	}
	unknown := !known

	var err error
	if r.Tier == classifierTier {
		// Routing overhead: add the tokens (and cost) to the request's row.
		_, err = l.db.ExecContext(ctx,
			`INSERT INTO usage_events (request_id, created_at, user_name, route, routing_tokens, cost, cost_unknown, currency)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(request_id) DO UPDATE SET
			   routing_tokens = COALESCE(usage_events.routing_tokens, 0) + excluded.routing_tokens,
			 `+addCost,
			requestID, at.Unix(), r.Origin, r.RouteID, r.PromptTokens+r.CompletionTokens,
			costV, unknown, currency)
	} else {
		_, err = l.db.ExecContext(ctx,
			`INSERT INTO usage_events (request_id, created_at, user_name, route, model, target, tier,
			   prompt_tokens, cached_tokens, cache_creation_tokens, completion_tokens, reasoning_tokens,
			   cost, cost_unknown, currency)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(request_id) DO UPDATE SET
			   user_name = CASE WHEN usage_events.user_name != '' THEN usage_events.user_name ELSE excluded.user_name END,
			   route = CASE WHEN usage_events.route != '' THEN usage_events.route ELSE excluded.route END,
			   model = excluded.model, target = excluded.target, tier = excluded.tier,
			   prompt_tokens = excluded.prompt_tokens, cached_tokens = excluded.cached_tokens,
			   cache_creation_tokens = excluded.cache_creation_tokens,
			   completion_tokens = excluded.completion_tokens, reasoning_tokens = excluded.reasoning_tokens,
			 `+addCost,
			requestID, at.Unix(), r.Origin, r.RouteID, r.Model, target, tier,
			r.PromptTokens, r.CachedTokens, r.CacheCreationTokens, r.CompletionTokens, r.ReasoningTokens,
			costV, unknown, currency)
	}
	if err != nil {
		slog.Error("writing usage row", "err", err)
	}
}

// RelabelUser moves the usage of a user deleted at the given time, per-request rows and
// monthly totals, from their name to label. The history stays readable under the label,
// and someone given the name later starts with none of it. It runs behind the writes
// already queued, so their last requests move too, and requests of theirs still running
// are filed under the label when they finish. It waits for room in the queue rather than
// being dropped.
func (l *Ledger) RelabelUser(userID int64, name, label string, at time.Time) {
	l.queue <- func(ctx context.Context) {
		for id, d := range l.deleted {
			if time.Since(d.at) > keepDeleted {
				delete(l.deleted, id)
			}
		}
		l.deleted[userID] = deletion{label: label, at: at}
		if err := l.relabel(ctx, name, label); err != nil {
			slog.Error("relabelling a deleted user's usage", "user", name, "err", err)
		}
	}
}

func (l *Ledger) relabel(ctx context.Context, name, label string) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"usage_events", "usage_monthly"} {
		if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET user_name = ? WHERE user_name = ?`, label, name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Prune folds per-request rows older than the retention period into monthly per-user
// totals and deletes them.
func (l *Ledger) Prune(ctx context.Context, days int) error {
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO usage_monthly (month, user_name, requests, errors, prompt_tokens, cached_tokens, completion_tokens, cost)
		 SELECT strftime('%Y-%m', created_at, 'unixepoch'), user_name, COUNT(*),
		        SUM(status >= 400), COALESCE(SUM(prompt_tokens), 0), COALESCE(SUM(cached_tokens), 0),
		        COALESCE(SUM(completion_tokens), 0),
		        CASE WHEN MAX(cost_unknown) = 1 THEN NULL ELSE SUM(cost) END
		 FROM usage_events WHERE created_at < ? GROUP BY 1, 2
		 ON CONFLICT(month, user_name) DO UPDATE SET
		   requests = requests + excluded.requests, errors = errors + excluded.errors,
		   prompt_tokens = prompt_tokens + excluded.prompt_tokens,
		   cached_tokens = cached_tokens + excluded.cached_tokens,
		   completion_tokens = completion_tokens + excluded.completion_tokens,
		   cost = CASE WHEN usage_monthly.cost IS NULL OR excluded.cost IS NULL THEN NULL
		               ELSE usage_monthly.cost + excluded.cost END`, cutoff)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM usage_events WHERE created_at < ?`, cutoff); err != nil {
		return err
	}
	return tx.Commit()
}
