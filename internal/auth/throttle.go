package auth

import (
	"strings"
	"sync"
	"time"
)

// Login throttling: at most 5 failures per account and 20 per client IP in a
// 15-minute window. Kept in memory: a restart clears it, which is acceptable because a
// restart is slow compared with the attempts it would allow.
const (
	throttleWindow     = 15 * time.Minute
	maxFailuresAccount = 5
	maxFailuresIP      = 20
)

type Throttle struct {
	mu       sync.Mutex
	accounts map[string][]time.Time
	ips      map[string][]time.Time
	now      func() time.Time
}

func NewThrottle() *Throttle {
	return &Throttle{accounts: map[string][]time.Time{}, ips: map[string][]time.Time{}, now: time.Now}
}

// Allow reports whether another attempt is allowed for this account and IP.
func (t *Throttle) Allow(account, ip string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	account = strings.ToLower(account)
	return len(t.recent(t.accounts, account)) < maxFailuresAccount &&
		len(t.recent(t.ips, ip)) < maxFailuresIP
}

// Fail records a failed attempt.
func (t *Throttle) Fail(account, ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	account = strings.ToLower(account)
	t.accounts[account] = append(t.recent(t.accounts, account), now)
	t.ips[ip] = append(t.recent(t.ips, ip), now)
}

// Reset clears an account's failures after a successful login.
func (t *Throttle) Reset(account string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.accounts, strings.ToLower(account))
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
