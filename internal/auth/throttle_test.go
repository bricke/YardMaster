package auth

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func fakeClock(t *Throttle) *time.Time {
	now := time.Unix(1790000000, 0)
	t.now = func() time.Time { return now }
	return &now
}

func TestThrottleSweepsExpiredEntries(t *testing.T) {
	th := NewThrottle()
	now := fakeClock(th)
	// Made-up usernames from many addresses, each tried once and never again.
	for i := range 1000 {
		th.Fail(fmt.Sprintf("user%d", i), fmt.Sprintf("10.0.%d.%d", i/256, i%256))
	}
	*now = now.Add(throttleWindow + sweepEvery)
	th.Fail("mallory", "10.9.9.9")
	if len(th.accounts) != 1 || len(th.ips) != 1 {
		t.Errorf("after the window: %d accounts, %d IPs kept; want 1 and 1", len(th.accounts), len(th.ips))
	}
}

func TestThrottleUnknownNamesLockLikeAccounts(t *testing.T) {
	// A made-up username is throttled like a real one, so the throttle can't tell an
	// attacker which accounts exist.
	th := NewThrottle()
	fakeClock(th)
	for i := range maxFailuresAccount {
		th.Fail("nobody", fmt.Sprintf("10.0.0.%d", i))
	}
	if th.Allow("nobody", "10.1.1.1") {
		t.Error("an unknown username was never throttled")
	}
}

func TestThrottleImpossibleNamesCountAgainstTheIP(t *testing.T) {
	th := NewThrottle()
	fakeClock(th)
	long := strings.Repeat("a", 5000)
	for range maxFailuresIP {
		th.Fail(long, "10.0.0.1")
		th.Fail("not a username!", "10.0.0.1")
	}
	if len(th.accounts) != 0 {
		t.Errorf("%d entries kept for names that can't be usernames", len(th.accounts))
	}
	if th.Allow("admin", "10.0.0.1") {
		t.Error("the IP limit didn't apply")
	}
	if !th.Allow("admin", "10.0.0.2") {
		t.Error("another IP was throttled")
	}
}
