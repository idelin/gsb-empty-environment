package lease

import (
	"errors"
	"testing"
	"time"
)

func TestAcquireAndDynamicRenew(t *testing.T) {
	clock := NewManualClock(time.Now())
	c := NewCoordinator(clock)

	l, err := c.Acquire("orders", "A", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if l.Term != 1 || l.HolderID != "A" {
		t.Fatalf("unexpected lease: %+v", l)
	}

	// Dynamic renewal: a different, larger TTL replaces the old expiry.
	clock.Advance(80 * time.Millisecond)
	l, err = c.Renew("orders", "A", l.Term, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if want := clock.Now().Add(500 * time.Millisecond); !l.ExpiresAt.Equal(want) {
		t.Fatalf("expiry = %v, want %v", l.ExpiresAt, want)
	}
	if l.Renewals != 1 {
		t.Fatalf("renewals = %d, want 1", l.Renewals)
	}

	// Well past the original 100ms TTL, the lease is still valid.
	clock.Advance(200 * time.Millisecond)
	if err := c.Validate("orders", "A", l.Term); err != nil {
		t.Fatalf("validate after dynamic renew: %v", err)
	}
}

func TestAcquireConflictWhileHeld(t *testing.T) {
	clock := NewManualClock(time.Now())
	c := NewCoordinator(clock)
	if _, err := c.Acquire("orders", "A", time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Acquire("orders", "B", time.Second); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("err = %v, want ErrLeaseHeld", err)
	}
	// After natural expiry another node may acquire, with a bumped term.
	clock.Advance(2 * time.Second)
	l, err := c.Acquire("orders", "B", time.Second)
	if err != nil {
		t.Fatalf("acquire after expiry: %v", err)
	}
	if l.Term != 2 {
		t.Fatalf("term = %d, want 2", l.Term)
	}
}

func TestRenewFailures(t *testing.T) {
	clock := NewManualClock(time.Now())
	c := NewCoordinator(clock)
	l, _ := c.Acquire("orders", "A", 100*time.Millisecond)

	if _, err := c.Renew("orders", "B", l.Term, time.Second); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("wrong holder: err = %v", err)
	}
	if _, err := c.Renew("orders", "A", l.Term+9, time.Second); !errors.Is(err, ErrInvalidTerm) {
		t.Fatalf("wrong term: err = %v", err)
	}
	clock.Advance(200 * time.Millisecond)
	if _, err := c.Renew("orders", "A", l.Term, time.Second); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("expired: err = %v", err)
	}
}

func TestHandoverInheritsRemainingContext(t *testing.T) {
	clock := NewManualClock(time.Now())
	c := NewCoordinator(clock)
	l, err := c.Acquire("orders", "A", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.PutContext("orders", "A", l.Term, "checkpoint", "offset=1024"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(400 * time.Millisecond)

	pkg, err := c.PrepareHandover("orders", "A", l.Term, "B")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if pkg.Term != l.Term+1 {
		t.Fatalf("package term = %d, want %d", pkg.Term, l.Term+1)
	}
	if pkg.RemainingTTL != 600*time.Millisecond {
		t.Fatalf("remaining TTL = %v, want 600ms", pkg.RemainingTTL)
	}

	nl, err := c.CompleteHandover(pkg, "B")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if nl.HolderID != "B" || nl.Term != 2 {
		t.Fatalf("new lease = %+v", nl)
	}
	if !nl.ExpiresAt.Equal(pkg.ExpiresAt) {
		t.Fatalf("candidate did not inherit remaining lease window: %v vs %v", nl.ExpiresAt, pkg.ExpiresAt)
	}
	if nl.Context["checkpoint"] != "offset=1024" {
		t.Fatalf("context not inherited: %v", nl.Context)
	}

	// Old holder is fenced out immediately; new holder can serve writes.
	if err := c.Validate("orders", "A", 1); !errors.Is(err, ErrInvalidTerm) {
		t.Fatalf("stale holder: err = %v, want ErrInvalidTerm", err)
	}
	if err := c.Validate("orders", "B", 2); err != nil {
		t.Fatalf("new holder validate: %v", err)
	}
}

func TestHandoverRejectsWrongCandidate(t *testing.T) {
	clock := NewManualClock(time.Now())
	c := NewCoordinator(clock)
	l, _ := c.Acquire("orders", "A", time.Second)

	pkg, err := c.PrepareHandover("orders", "A", l.Term, "B")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CompleteHandover(pkg, "C"); !errors.Is(err, ErrInvalidCandidate) {
		t.Fatalf("err = %v, want ErrInvalidCandidate", err)
	}
}

func TestHandoverPackageExpires(t *testing.T) {
	clock := NewManualClock(time.Now())
	c := NewCoordinator(clock)
	l, _ := c.Acquire("orders", "A", 100*time.Millisecond)

	pkg, err := c.PrepareHandover("orders", "A", l.Term, "B")
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(200 * time.Millisecond) // candidate too slow; lease expired
	if _, err := c.CompleteHandover(pkg, "B"); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("err = %v, want ErrLeaseExpired", err)
	}
}

func TestLeaseFrozenDuringHandover(t *testing.T) {
	clock := NewManualClock(time.Now())
	c := NewCoordinator(clock)
	l, _ := c.Acquire("orders", "A", time.Second)

	if _, err := c.PrepareHandover("orders", "A", l.Term, "B"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Renew("orders", "A", l.Term, time.Second); !errors.Is(err, ErrLeaseFrozen) {
		t.Fatalf("renew during handover: err = %v, want ErrLeaseFrozen", err)
	}
	if err := c.Validate("orders", "A", l.Term); !errors.Is(err, ErrLeaseFrozen) {
		t.Fatalf("write during handover: err = %v, want ErrLeaseFrozen", err)
	}
	if _, err := c.PrepareHandover("orders", "A", l.Term, "C"); !errors.Is(err, ErrLeaseFrozen) {
		t.Fatalf("second handover: err = %v, want ErrLeaseFrozen", err)
	}
}

func TestAbortHandoverRestoresHolder(t *testing.T) {
	clock := NewManualClock(time.Now())
	c := NewCoordinator(clock)
	l, _ := c.Acquire("orders", "A", time.Second)

	if _, err := c.PrepareHandover("orders", "A", l.Term, "B"); err != nil {
		t.Fatal(err)
	}
	if err := c.AbortHandover("orders", "A", l.Term); err != nil {
		t.Fatalf("abort: %v", err)
	}
	// Holder resumes serving with the original term.
	if err := c.Validate("orders", "A", l.Term); err != nil {
		t.Fatalf("validate after abort: %v", err)
	}
	if _, err := c.Renew("orders", "A", l.Term, time.Second); err != nil {
		t.Fatalf("renew after abort: %v", err)
	}
}
