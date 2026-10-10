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
	if th.Begin("nobody", "10.1.1.1") != ErrThrottled {
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
	if th.Begin("admin", "10.0.0.1") != ErrThrottled {
		t.Error("the IP limit didn't apply")
	}
	if th.Begin("admin", "10.0.0.2") != nil {
		t.Error("another IP was throttled")
	}
}

func TestThrottleCountsAttemptsInProgress(t *testing.T) {
	th := NewThrottle()
	fakeClock(th)
	// Parallel guesses at one account, from many addresses.
	for i := range maxFailuresAccount {
		if th.Begin("admin", fmt.Sprintf("10.0.0.%d", i)) != nil {
			t.Fatalf("attempt %d refused", i)
		}
	}
	if err := th.Begin("admin", "10.0.1.1"); err != ErrBusy {
		t.Errorf("an attempt beyond the account limit while the others were still running: %v, want ErrBusy", err)
	}
	// Parallel guesses from one address, at many accounts.
	for i := range maxFailuresIP {
		if th.Begin(fmt.Sprintf("user%d", i), "10.9.9.9") != nil {
			t.Fatalf("attempt %d refused", i)
		}
	}
	if err := th.Begin("someone", "10.9.9.9"); err != ErrBusy {
		t.Errorf("an attempt beyond the IP limit while the others were still running: %v, want ErrBusy", err)
	}
	// Attempts that end without failing leave nothing behind, so many people signing in
	// from one office address are never throttled.
	for i := range maxFailuresAccount {
		th.Done("admin", fmt.Sprintf("10.0.0.%d", i))
	}
	for i := range maxFailuresIP {
		th.Done(fmt.Sprintf("user%d", i), "10.9.9.9")
	}
	if len(th.pendingAccounts) != 0 || len(th.pendingIPs) != 0 {
		t.Errorf("attempts still counted: %v %v", th.pendingAccounts, th.pendingIPs)
	}
	if th.Begin("admin", "10.9.9.9") != nil {
		t.Error("refused after every attempt ended")
	}
}
