package usage

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Filter narrows a usage query. Days counts UTC calendar days including today.
type Filter struct {
	Days     int
	UserName string // "" for everyone (admin only)
	TokenID  int64
}

func (f Filter) where() (string, []any) {
	days := f.Days
	if days <= 0 || days > 400 {
		days = 30
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -(days - 1)).Unix()
	w := []string{"created_at >= ?"}
	args := []any{start}
	if f.UserName != "" {
		w = append(w, "user_name = ?")
		args = append(args, f.UserName)
	}
	if f.TokenID != 0 {
		w = append(w, "token_id = ?")
		args = append(args, f.TokenID)
	}
	return " WHERE " + strings.Join(w, " AND "), args
}

// Totals sums a group of requests. Token counts include only requests that reported
// them; Cost is nil when nothing was priced or any request used an unpriced model.
type Totals struct {
	Key              string   `json:"key"`
	Label            string   `json:"label,omitempty"`
	Requests         int64    `json:"requests"`
	Errors           int64    `json:"errors"`
	PromptTokens     int64    `json:"prompt_tokens"`
	CachedTokens     int64    `json:"cached_tokens"`
	CompletionTokens int64    `json:"completion_tokens"`
	RoutingTokens    int64    `json:"routing_tokens"`
	Cost             *float64 `json:"cost"`
	Currency         string   `json:"currency,omitempty"`
	LastAt           int64    `json:"last_at,omitempty"`
}

const totalsColumns = `COUNT(*), COALESCE(SUM(status >= 400), 0),
	COALESCE(SUM(prompt_tokens), 0), COALESCE(SUM(cached_tokens), 0),
	COALESCE(SUM(completion_tokens), 0), COALESCE(SUM(routing_tokens), 0),
	CASE WHEN MAX(cost_unknown) = 1 OR COUNT(cost) = 0 THEN NULL ELSE SUM(cost) END,
	COALESCE(MAX(currency), ''), COALESCE(MAX(created_at), 0)`

func scanTotals(row interface{ Scan(...any) error }, key *string) (Totals, error) {
	var t Totals
	var cost sql.NullFloat64
	dest := []any{&t.Requests, &t.Errors, &t.PromptTokens, &t.CachedTokens, &t.CompletionTokens,
		&t.RoutingTokens, &cost, &t.Currency, &t.LastAt}
	if key != nil {
		dest = append([]any{key}, dest...)
	}
	if err := row.Scan(dest...); err != nil {
		return t, err
	}
	if cost.Valid {
		t.Cost = &cost.Float64
	}
	if key != nil {
		t.Key = *key
	}
	return t, nil
}

// Summary is the usage view for one person or everyone.
type Summary struct {
	Days    int      `json:"days"`
	Totals  Totals   `json:"totals"`
	ByDay   []Totals `json:"by_day"`
	ByRoute []Totals `json:"by_route"`
	ByModel []Totals `json:"by_model"`
	ByTier  []Totals `json:"by_tier"`
	// Tokens served by the weak tier on routes that have one: traffic kept off the
	// strong model.
	KeptOffStrong int64 `json:"kept_off_strong"`
}

// Summarize builds a Summary for the filter.
func (q *Ledger) Summarize(ctx context.Context, f Filter) (*Summary, error) {
	where, args := f.where()
	s := &Summary{Days: max(f.Days, 1)}
	row := q.db.QueryRowContext(ctx, `SELECT `+totalsColumns+` FROM usage_events`+where, args...)
	var err error
	if s.Totals, err = scanTotals(row, nil); err != nil {
		return nil, err
	}
	if s.ByDay, err = q.grouped(ctx, `strftime('%Y-%m-%d', created_at, 'unixepoch')`, where, args, "1"); err != nil {
		return nil, err
	}
	if s.ByRoute, err = q.grouped(ctx, `route`, where, args, "2 DESC"); err != nil {
		return nil, err
	}
	if s.ByModel, err = q.grouped(ctx, `model`, where, args, "2 DESC"); err != nil {
		return nil, err
	}
	if s.ByTier, err = q.grouped(ctx, `tier`, where+" AND tier != ''", args, "1"); err != nil {
		return nil, err
	}
	err = q.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(COALESCE(prompt_tokens,0) + COALESCE(completion_tokens,0)), 0)
		 FROM usage_events`+where+` AND tier = 'weak'`, args...).Scan(&s.KeptOffStrong)
	if err != nil {
		return nil, err
	}
	s.ByDay = fillDays(s.ByDay, s.Days)
	return s, nil
}

// ByUser returns one row per person (admin view).
func (q *Ledger) ByUser(ctx context.Context, f Filter) ([]Totals, error) {
	where, args := f.where()
	return q.grouped(ctx, `user_name`, where, args, "2 DESC")
}

// ByToken returns one row per token of one user (the user's own view).
func (q *Ledger) ByToken(ctx context.Context, f Filter) ([]Totals, error) {
	where, args := f.where()
	return q.grouped(ctx, `COALESCE(token_id, 0)`, where, args, "2 DESC")
}

func (q *Ledger) grouped(ctx context.Context, key, where string, args []any, order string) ([]Totals, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT `+key+`, `+totalsColumns+` FROM usage_events`+where+` GROUP BY 1 ORDER BY `+order+` LIMIT 500`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Totals{}
	for rows.Next() {
		var key string
		t, err := scanTotals(rows, &key)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// fillDays adds empty days so charts have one point per day.
func fillDays(rows []Totals, days int) []Totals {
	have := map[string]Totals{}
	for _, r := range rows {
		have[r.Key] = r
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	out := make([]Totals, 0, days)
	for i := days - 1; i >= 0; i-- {
		key := today.AddDate(0, 0, -i).Format("2006-01-02")
		if r, ok := have[key]; ok {
			out = append(out, r)
		} else {
			out = append(out, Totals{Key: key})
		}
	}
	return out
}

// Request is one row, for the recent-requests and errors lists.
type Request struct {
	RequestID        string   `json:"request_id"`
	CreatedAt        int64    `json:"created_at"`
	UserName         string   `json:"user_name,omitempty"`
	TokenName        string   `json:"token_name"`
	Route            string   `json:"route"`
	Model            string   `json:"model"`
	Tier             string   `json:"tier"`
	PromptTokens     *int64   `json:"prompt_tokens"`
	CompletionTokens *int64   `json:"completion_tokens"`
	LatencyMS        *int64   `json:"latency_ms"`
	Status           *int64   `json:"status"`
	Error            string   `json:"error,omitempty"`
	Cost             *float64 `json:"cost"`
	Currency         string   `json:"currency,omitempty"`
}

// Recent lists the latest requests matching the filter; errorsOnly keeps failures.
func (q *Ledger) Recent(ctx context.Context, f Filter, errorsOnly bool, limit int) ([]Request, error) {
	where, args := f.where()
	if errorsOnly {
		where += " AND status >= 400"
	}
	rows, err := q.db.QueryContext(ctx,
		`SELECT request_id, created_at, user_name, token_name, route, model, tier, prompt_tokens,
		   completion_tokens, latency_ms, status, error,
		   CASE WHEN cost_unknown = 1 THEN NULL ELSE cost END, currency
		 FROM usage_events`+where+` ORDER BY created_at DESC, id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		var r Request
		var prompt, completion, latency, status sql.NullInt64
		var cost sql.NullFloat64
		if err := rows.Scan(&r.RequestID, &r.CreatedAt, &r.UserName, &r.TokenName, &r.Route, &r.Model,
			&r.Tier, &prompt, &completion, &latency, &status, &r.Error, &cost, &r.Currency); err != nil {
			return nil, err
		}
		r.PromptTokens, r.CompletionTokens = ptr(prompt), ptr(completion)
		r.LatencyMS, r.Status = ptr(latency), ptr(status)
		if cost.Valid {
			r.Cost = &cost.Float64
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Monthly returns per-month totals for one user or everyone, from both the monthly
// table (expired rows) and the live rows.
func (q *Ledger) Monthly(ctx context.Context, userName string) ([]Totals, error) {
	where, args := "", []any{}
	if userName != "" {
		where, args = " WHERE user_name = ?", []any{userName, userName}
	}
	rows, err := q.db.QueryContext(ctx,
		`SELECT month, SUM(requests), SUM(errors), SUM(prompt), SUM(cached), SUM(completion) FROM (
		   SELECT month, requests, errors, prompt_tokens AS prompt, cached_tokens AS cached,
		          completion_tokens AS completion FROM usage_monthly`+where+`
		   UNION ALL
		   SELECT strftime('%Y-%m', created_at, 'unixepoch'), COUNT(*), SUM(status >= 400),
		          COALESCE(SUM(prompt_tokens), 0), COALESCE(SUM(cached_tokens), 0),
		          COALESCE(SUM(completion_tokens), 0) FROM usage_events`+where+` GROUP BY 1
		 ) GROUP BY month ORDER BY month DESC LIMIT 36`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Totals{}
	for rows.Next() {
		var t Totals
		var errs sql.NullInt64
		if err := rows.Scan(&t.Key, &t.Requests, &errs, &t.PromptTokens, &t.CachedTokens, &t.CompletionTokens); err != nil {
			return nil, err
		}
		t.Errors = errs.Int64
		out = append(out, t)
	}
	return out, rows.Err()
}

func ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}
