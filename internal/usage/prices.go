package usage

import (
	"context"
	"errors"
	"strings"
	"sync"

	"yardmaster/internal/store"
)

// Price is what one target costs, per million tokens. Targets without a price
// have no cost; they're never shown as free.
type Price struct {
	Target     string  `json:"target"`
	Currency   string  `json:"currency"`
	Input      float64 `json:"input"`
	Cached     float64 `json:"cached"`
	CacheWrite float64 `json:"cache_write"`
	Output     float64 `json:"output"`
	UpdatedAt  int64   `json:"updated_at"`
}

// Prices caches the price table in memory; it changes rarely and is read per record.
type Prices struct {
	db *store.DB
	mu sync.RWMutex
	m  map[string]Price
}

func NewPrices(ctx context.Context, db *store.DB) (*Prices, error) {
	p := &Prices{db: db}
	return p, p.reload(ctx)
}

func (p *Prices) reload(ctx context.Context) error {
	rows, err := p.db.QueryContext(ctx, `SELECT target, currency, input, cached, cache_write, output, updated_at FROM prices`)
	if err != nil {
		return err
	}
	defer rows.Close()
	m := map[string]Price{}
	for rows.Next() {
		var pr Price
		if err := rows.Scan(&pr.Target, &pr.Currency, &pr.Input, &pr.Cached, &pr.CacheWrite, &pr.Output, &pr.UpdatedAt); err != nil {
			return err
		}
		m[pr.Target] = pr
	}
	p.mu.Lock()
	p.m = m
	p.mu.Unlock()
	return rows.Err()
}

// All returns every price.
func (p *Prices) All() []Price {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := []Price{}
	for _, pr := range p.m {
		out = append(out, pr)
	}
	return out
}

// Set stores a target's price. It applies to requests from now on; the ledger keeps each
// request's cost at the price in effect when it was recorded.
func (p *Prices) Set(ctx context.Context, pr Price) error {
	pr.Currency = strings.ToUpper(strings.TrimSpace(pr.Currency))
	if pr.Target == "" {
		return errors.New("pick a model")
	}
	if len(pr.Currency) != 3 {
		return errors.New("currency must be a three-letter code like USD")
	}
	for _, v := range []float64{pr.Input, pr.Cached, pr.CacheWrite, pr.Output} {
		if v < 0 {
			return errors.New("prices can't be negative")
		}
	}
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO prices (target, currency, input, cached, cache_write, output, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(target) DO UPDATE SET currency = excluded.currency, input = excluded.input,
		   cached = excluded.cached, cache_write = excluded.cache_write, output = excluded.output,
		   updated_at = excluded.updated_at`,
		pr.Target, pr.Currency, pr.Input, pr.Cached, pr.CacheWrite, pr.Output, store.Now())
	if err != nil {
		return err
	}
	return p.reload(ctx)
}

// Delete removes a target's price.
func (p *Prices) Delete(ctx context.Context, target string) error {
	if _, err := p.db.ExecContext(ctx, `DELETE FROM prices WHERE target = ?`, target); err != nil {
		return err
	}
	return p.reload(ctx)
}

// Cost prices one routing record. known is false when the target has no price.
//
// Switchyard's prompt_tokens include cached and cache-creation tokens, so those are
// priced at their own rates and the rest at the input rate.
func (p *Prices) Cost(target string, r Record) (cost float64, known bool, currency string) {
	p.mu.RLock()
	pr, ok := p.m[target]
	p.mu.RUnlock()
	if !ok || target == "" {
		return 0, false, ""
	}
	fresh := max(r.PromptTokens-r.CachedTokens-r.CacheCreationTokens, 0)
	cost = (float64(fresh)*pr.Input +
		float64(r.CachedTokens)*pr.Cached +
		float64(r.CacheCreationTokens)*pr.CacheWrite +
		float64(r.CompletionTokens)*pr.Output) / 1e6
	return cost, true, pr.Currency
}
