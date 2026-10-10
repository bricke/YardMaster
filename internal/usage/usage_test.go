package usage

import (
	"context"
	"testing"
	"time"

	"yardmaster/internal/deploy"
	"yardmaster/internal/store"
)

const config = `schema_version = 1
[llm_clients.p]
format = "openai_chat"
base_url = "http://x"
[targets.strong]
id = "big"
llm_client = "p"
[targets.weak]
id = "small"
llm_client = "p"
[routes.a]
id = "smart"
type = "llm_classifier"
strong_target = "strong"
weak_target = "weak"
classifier_target = "weak"
base_threshold = 0.5
`

func setup(t *testing.T) (*Ledger, *Prices, context.Context) {
	ctx := context.Background()
	db := store.OpenTest(t)
	prices, err := NewPrices(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	p, err := deploy.Parse(config)
	if err != nil {
		t.Fatal(err)
	}
	return NewLedger(db, prices, func() *deploy.Parsed { return p }), prices, ctx
}

// drain runs queued writes synchronously.
func drain(l *Ledger) {
	for {
		select {
		case job := <-l.queue:
			job(context.Background())
		default:
			return
		}
	}
}

func TestJoinGatewayAndRoutingLog(t *testing.T) {
	l, prices, ctx := setup(t)
	prices.Set(ctx, Price{Target: "weak", Currency: "usd", Input: 1, Cached: 0.5, Output: 2})
	now := time.Now()
	// The routing record can arrive before or after the gateway's event.
	l.AddRecord("req-1", Record{TS: now.Format(time.RFC3339Nano), RouteID: "smart", Origin: "alice", Model: "small", Tier: "classifier", PromptTokens: 100, CompletionTokens: 10})
	l.AddRecord("req-1", Record{TS: now.Format(time.RFC3339Nano), RouteID: "smart", Origin: "alice", Model: "small", PromptTokens: 1000, CachedTokens: 400, CompletionTokens: 200})
	l.AddGateway(GatewayEvent{RequestID: "req-1", At: now, UserID: 7, UserName: "alice", TokenID: 3, TokenName: "laptop", Route: "smart", Status: 200, LatencyMS: 900})
	drain(l)

	rows, err := l.Recent(ctx, Filter{Days: 1, UserName: "alice"}, false, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows %v %v", rows, err)
	}
	r := rows[0]
	if r.Model != "small" || r.Tier != "weak" || r.TokenName != "laptop" || *r.Status != 200 || *r.PromptTokens != 1000 {
		t.Fatalf("joined row %+v", r)
	}
	// 600 fresh × 1 + 400 cached × 0.5 + 200 out × 2, plus the judge call 100 × 1 + 10 × 2, per million.
	want := (600 + 200 + 400 + 100 + 20) / 1e6
	if r.Cost == nil || abs(*r.Cost-want) > 1e-12 || r.Currency != "USD" {
		t.Fatalf("cost %v %s, want %v USD", r.Cost, r.Currency, want)
	}
	s, _ := l.Summarize(ctx, Filter{Days: 1})
	if s.Totals.RoutingTokens != 110 || s.KeptOffStrong != 1200 {
		t.Fatalf("totals %+v kept %d", s.Totals, s.KeptOffStrong)
	}
}

func TestUnpricedModelMakesCostUnknown(t *testing.T) {
	l, prices, ctx := setup(t)
	prices.Set(ctx, Price{Target: "weak", Currency: "USD", Input: 1, Output: 1})
	now := time.Now().Format(time.RFC3339Nano)
	l.AddRecord("a", Record{TS: now, RouteID: "smart", Origin: "bob", Model: "small", PromptTokens: 10})
	l.AddRecord("b", Record{TS: now, RouteID: "smart", Origin: "bob", Model: "big", PromptTokens: 10})
	drain(l)
	s, _ := l.Summarize(ctx, Filter{Days: 1, UserName: "bob"})
	if s.Totals.Cost != nil {
		t.Fatalf("total cost %v should be unknown when a model has no price", *s.Totals.Cost)
	}
}

func TestPruneKeepsMonthlyTotals(t *testing.T) {
	l, _, ctx := setup(t)
	old := time.Now().AddDate(0, 0, -120)
	l.AddGateway(GatewayEvent{RequestID: "old", At: old, UserName: "carol", Status: 200})
	l.AddRecord("old", Record{TS: old.Format(time.RFC3339Nano), RouteID: "smart", Origin: "carol", Model: "small", PromptTokens: 50, CompletionTokens: 5})
	l.AddGateway(GatewayEvent{RequestID: "new", At: time.Now(), UserName: "carol", Status: 500})
	drain(l)
	if err := l.Prune(ctx, 90); err != nil {
		t.Fatal(err)
	}
	var n int
	l.db.QueryRow(`SELECT COUNT(*) FROM usage_events`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d rows left, want 1", n)
	}
	months, _ := l.Monthly(ctx, "carol")
	var total, prompt int64
	for _, m := range months {
		total += m.Requests
		prompt += m.PromptTokens
	}
	if total != 2 || prompt != 50 {
		t.Fatalf("monthly totals lost data: requests %d prompt %d", total, prompt)
	}
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func TestSummaryDaysMatchTheQuery(t *testing.T) {
	l, _, ctx := setup(t)
	for _, c := range []struct{ asked, want int }{{7, 7}, {0, 30}, {-3, 30}, {1000, 30}} {
		s, err := l.Summarize(ctx, Filter{Days: c.asked})
		if err != nil {
			t.Fatal(err)
		}
		if s.Days != c.want || len(s.ByDay) != c.want {
			t.Errorf("days=%d: summary covers %d days with %d chart points, want %d", c.asked, s.Days, len(s.ByDay), c.want)
		}
	}
}

func TestRelabelUserMovesAllTheirUsage(t *testing.T) {
	l, _, ctx := setup(t)
	old := time.Now().AddDate(0, 0, -120)
	l.AddGateway(GatewayEvent{RequestID: "old", At: old, UserName: "bob", Status: 200})
	l.AddGateway(GatewayEvent{RequestID: "new", At: time.Now(), UserName: "bob", Status: 200})
	l.AddGateway(GatewayEvent{RequestID: "other", At: time.Now(), UserName: "bobby", Status: 200})
	drain(l)
	if err := l.Prune(ctx, 90); err != nil {
		t.Fatal(err)
	}
	deletedAt := time.Now()
	l.RelabelUser(7, "bob", "bob (deleted)", deletedAt)
	drain(l)
	// A request bob started before the deletion finishes after it; a new bob, given the
	// same name and, as SQLite may, the same ID, starts one after it.
	l.AddGateway(GatewayEvent{RequestID: "in-flight", At: deletedAt.Add(-time.Minute), UserID: 7, UserName: "bob", Status: 200})
	l.AddGateway(GatewayEvent{RequestID: "new-bob", At: deletedAt.Add(time.Second), UserID: 7, UserName: "bob", Status: 200})
	drain(l)

	// Someone given the name later sees none of it, neither recent nor monthly.
	if rows, _ := l.Recent(ctx, Filter{Days: 30, UserName: "bob"}, false, 10); len(rows) != 1 || rows[0].RequestID != "new-bob" {
		t.Errorf("under the name: %+v, want only the new bob's request", rows)
	}
	if m, _ := l.Monthly(ctx, "bob"); len(m) != 1 || m[0].Requests != 1 {
		t.Errorf("monthly totals under the name: %+v, want only the new bob's request", m)
	}
	// The history stays readable under the label, and nobody else's moves.
	var total int64
	m, _ := l.Monthly(ctx, "bob (deleted)")
	for _, row := range m {
		total += row.Requests
	}
	if total != 3 {
		t.Errorf("%d requests under the label, want 3", total)
	}
	if s, _ := l.Summarize(ctx, Filter{Days: 30, UserName: "bobby"}); s.Totals.Requests != 1 {
		t.Errorf("another user's usage moved: %d left", s.Totals.Requests)
	}
}
