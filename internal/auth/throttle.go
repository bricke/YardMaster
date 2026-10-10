package auth

import (
	"strings"
	"sync"
	"time"
)

// Login throttling: at most 5 failures per account and 20 per client IP in a
// 15-minute window. Kept in memory: a restart clears it, which is acceptable because a
// restart is slow compared with the attempts it would allow.
//
// Attempts still being checked count against the limits too. A password check takes a
// quarter of a second, so without that, a burst of parallel guesses would all be allowed
// before the first of them failed.
//
// Memory stays proportional to recent failures: expired entries are swept regularly, and
// a name that can't be a username counts against the IP only. Unknown but valid names are
// tracked like real ones, so the throttle doesn't reveal which accounts exist.
const (
	throttleWindow     = 15 * time.Minute
	maxFailuresAccount = 5
	maxFailuresIP      = 20
	sweepEvery         = time.Minute
)

type Throttle struct {
	mu        sync.Mutex
	accounts  map[string][]time.Time
	ips       map[string][]time.Time
	lastSweep time.Time
	now       func() time.Time
	// Attempts begun and not yet done, per account and per IP.
	pendingAccounts map[string]int
	pendingIPs      map[string]int
}

func NewThrottle() *Throttle {
	return &Throttle{accounts: map[string][]time.Time{}, ips: map[string][]time.Time{}, now: time.Now,
		pendingAccounts: map[string]int{}, pendingIPs: map[string]int{}}
}

// Begin starts an attempt for this account and IP and counts it as in progress until
// Done, or refuses it: ErrThrottled after too many failures, ErrBusy when the limit is
// reached only because other attempts are still being checked. Record a failure with Fail
// before Done, so the attempt is counted throughout.
func (t *Throttle) Begin(account, ip string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	key, valid := accountKey(account)
	var accountFailures int
	if valid {
		accountFailures = len(t.recent(t.accounts, key))
	}
	ipFailures := len(t.recent(t.ips, ip))
	if accountFailures >= maxFailuresAccount || ipFailures >= maxFailuresIP {
		return ErrThrottled
	}
	if valid && accountFailures+t.pendingAccounts[key] >= maxFailuresAccount ||
		ipFailures+t.pendingIPs[ip] >= maxFailuresIP {
		return ErrBusy
	}
	if valid {
		t.pendingAccounts[key]++
	}
	t.pendingIPs[ip]++
	return nil
}

// Done ends an attempt that Begin allowed.
func (t *Throttle) Done(account, ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if key, ok := accountKey(account); ok {
		release(t.pendingAccounts, key)
	}
	release(t.pendingIPs, ip)
}

func release(m map[string]int, key string) {
	if m[key] <= 1 {
		delete(m, key)
	} else {
		m[key]--
	}
}

// Fail records a failed attempt.
func (t *Throttle) Fail(account, ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if now.Sub(t.lastSweep) >= sweepEvery {
		t.sweep()
		t.lastSweep = now
	}
	if key, ok := accountKey(account); ok {
		t.accounts[key] = append(t.recent(t.accounts, key), now)
	}
	t.ips[ip] = append(t.recent(t.ips, ip), now)
}

// Reset clears an account's failures after a successful login.
func (t *Throttle) Reset(account string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if key, ok := accountKey(account); ok {
		delete(t.accounts, key)
	}
}

// accountKey is the key an account's failures are kept under, or false for a name that
// can't be a username.
func accountKey(name string) (string, bool) {
	if !ValidUsername(name) {
		return "", false
	}
	return strings.ToLower(name), true
}

// sweep drops every entry whose failures have all expired.
func (t *Throttle) sweep() {
	for key := range t.accounts {
		t.recent(t.accounts, key)
	}
	for key := range t.ips {
		t.recent(t.ips, key)
	}
}

// recent returns the failures still inside the window, dropping older ones.
func (t *Throttle) recent(m map[string][]time.Time, key string) []time.Time {
	cutoff := t.now().Add(-throttleWindow)
	times := m[key]
	i := 0
	for i < len(times) && times[i].Before(cutoff) {
		i++
	}
	if i == len(times) {
		delete(m, key)
		return nil
	}
	m[key] = times[i:]
	return m[key]
}
