package netsec_test

import (
	"errors"
	"testing"
	"time"

	netsec "example.org/netsec/q0348"
)

func newTestRing(t *testing.T, now time.Time) *netsec.KeyRing {
	t.Helper()
	ring := netsec.NewKeyRing(2)
	ring.AddKey("k0", 0)
	ring.AddKey("k1", 1)
	ring.AddKey("k2", 2)
	ring.AddKey("k3", 3)
	return ring
}

func TestRenewSubsetAndIssuance(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	ring := newTestRing(t, now)
	old := netsec.Ticket{
		Caps:      mustCaps(t, "allow rw pool=app namespace=prod"),
		ExpiresAt: now.Add(time.Hour),
		KeyID:     "k2",
	}
	auth := &netsec.Authority{Keys: ring, TTL: 30 * time.Minute}

	newCaps := mustCaps(t, "allow r pool=app namespace=prod")
	renewed, err := auth.Renew(old, newCaps, now)
	if err != nil {
		t.Fatalf("Renew reduced caps: %v", err)
	}
	if got, want := renewed.ExpiresAt, now.Add(30*time.Minute); !got.Equal(want) {
		t.Fatalf("renewed expiration = %s, want %s", got, want)
	}
	if renewed.KeyID != "k2" {
		t.Fatalf("renewed key = %q, want k2", renewed.KeyID)
	}
	if !renewed.Caps.IsSubsetOf(old.Caps) {
		t.Fatal("renewed caps are not a subset of the old caps")
	}
	if !old.Caps.Allows("app", "prod", netsec.PermWrite) {
		t.Fatal("Renew must not mutate the old ticket")
	}
	if renewed.Caps.Allows("app", "prod", netsec.PermWrite) {
		t.Fatal("renewed ticket unexpectedly regained write")
	}
}

func TestRenewalAmplificationRejected(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	ring := newTestRing(t, now)
	old := netsec.Ticket{
		Caps:      mustCaps(t, "allow rw pool=app namespace=prod"),
		ExpiresAt: now.Add(time.Hour),
		KeyID:     "k2",
	}
	auth := &netsec.Authority{Keys: ring}

	tests := []string{
		"allow rwx pool=app namespace=prod",
		"allow rw",
		"allow rw pool=app",
		"allow rw namespace=prod",
		"allow rw pool=other namespace=prod",
		"allow rw pool=app namespace=other",
		"allow rw pool=app namespace=prod, allow x pool=app",
	}
	for _, requested := range tests {
		t.Run(requested, func(t *testing.T) {
			newCaps := mustCaps(t, requested)
			if _, err := auth.Renew(old, newCaps, now); !errors.Is(err, netsec.ErrNotSubset) {
				t.Fatalf("Renew(%q) error = %v, want ErrNotSubset", requested, err)
			}
			if err := auth.ValidateRenewal(old, newCaps, now); !errors.Is(err, netsec.ErrNotSubset) {
				t.Fatalf("ValidateRenewal(%q) error = %v, want ErrNotSubset", requested, err)
			}
		})
	}

	if !errors.Is(netsec.ErrEscalation, netsec.ErrNotSubset) {
		t.Fatal("ErrEscalation should alias ErrNotSubset")
	}
}

func TestExpiredTicketRejected(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	ring := newTestRing(t, now)
	auth := &netsec.Authority{Keys: ring}
	caps := mustCaps(t, "allow r pool=app")

	for _, expiresAt := range []time.Time{
		zeroTime(),
		now.Add(-time.Second),
		now,
	} {
		ticket := netsec.Ticket{Caps: caps, ExpiresAt: expiresAt, KeyID: "k2"}
		if _, err := auth.Renew(ticket, caps, now); !errors.Is(err, netsec.ErrExpired) {
			t.Fatalf("Renew(expires=%s) error = %v, want ErrExpired", expiresAt, err)
		}
		if err := ticket.Validate(now, ring); !errors.Is(err, netsec.ErrExpired) {
			t.Fatalf("Validate(expires=%s) error = %v, want ErrExpired", expiresAt, err)
		}
	}

	valid := netsec.Ticket{Caps: caps, ExpiresAt: now.Add(time.Nanosecond), KeyID: "k2"}
	if err := valid.Validate(now, ring); err != nil {
		t.Fatalf("ticket expiring in the future was rejected: %v", err)
	}
}

func TestRotationWindow(t *testing.T) {
	ring := netsec.NewKeyRing(2)
	ring.AddKey("k0", 0)
	ring.AddKey("k1", 1)
	ring.AddKey("k2", 2)
	ring.AddKey("k3", 3)

	tests := []struct {
		key  string
		want bool
	}{
		{"k0", false},
		{"k1", true},
		{"k2", true},
		{"k3", false},
		{"unknown", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := ring.KeyActive(tt.key); got != tt.want {
			t.Fatalf("KeyActive(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}

	if ring.CurrentGeneration() != 2 || ring.Window() != 2 {
		t.Fatalf("ring current/window = %d/%d, want 2/2", ring.CurrentGeneration(), ring.Window())
	}
	if gen, ok := ring.KeyGeneration("k1"); !ok || gen != 1 {
		t.Fatalf("KeyGeneration(k1) = %d,%v, want 1,true", gen, ok)
	}

	if got := ring.Rotate("k4"); got != 3 {
		t.Fatalf("Rotate returned generation %d, want 3", got)
	}
	if ring.KeyActive("k1") {
		t.Fatal("k1 must retire once two newer generations exist")
	}
	if !ring.KeyActive("k2") || !ring.KeyActive("k4") {
		t.Fatal("current and previous generations must remain active")
	}
}

func TestRetiredAndUnknownKeyRejected(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	ring := newTestRing(t, now)
	auth := &netsec.Authority{Keys: ring}
	caps := mustCaps(t, "allow r pool=app")

	for _, keyID := range []string{"k0", "k3", "missing", ""} {
		ticket := netsec.Ticket{Caps: caps, ExpiresAt: now.Add(time.Hour), KeyID: keyID}
		if _, err := auth.Renew(ticket, caps, now); !errors.Is(err, netsec.ErrKeyRetired) {
			t.Fatalf("Renew(key=%q) error = %v, want ErrKeyRetired", keyID, err)
		}
	}

	previous := netsec.Ticket{Caps: caps, ExpiresAt: now.Add(time.Hour), KeyID: "k1"}
	if _, err := auth.Renew(previous, caps, now); err != nil {
		t.Fatalf("previous-generation key should be renewable: %v", err)
	}
}

func TestRenewalSigningKeyPolicy(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	ring := newTestRing(t, now)
	caps := mustCaps(t, "allow r pool=app")
	old := netsec.Ticket{Caps: caps, ExpiresAt: now.Add(time.Hour), KeyID: "k1"}

	auth := &netsec.Authority{Keys: ring, SigningKeyID: "k2"}
	renewed, err := auth.Renew(old, caps, now)
	if err != nil {
		t.Fatalf("Renew with active signing key: %v", err)
	}
	if renewed.KeyID != "k2" {
		t.Fatalf("renewed key = %q, want k2", renewed.KeyID)
	}

	retiredSigner := &netsec.Authority{Keys: ring, SigningKeyID: "k0"}
	if _, err := retiredSigner.Renew(old, caps, now); !errors.Is(err, netsec.ErrKeyRetired) {
		t.Fatalf("retired signing key error = %v, want ErrKeyRetired", err)
	}
}

func TestInvalidCapsRejectedDuringRenewal(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	ring := newTestRing(t, now)
	auth := &netsec.Authority{Keys: ring}
	valid := mustCaps(t, "allow r")
	invalid := netsec.Caps{{Permissions: 0}}

	ticket := netsec.Ticket{Caps: valid, ExpiresAt: now.Add(time.Hour), KeyID: "k2"}
	if _, err := auth.Renew(ticket, invalid, now); !errors.Is(err, netsec.ErrInvalidCaps) {
		t.Fatalf("invalid requested caps error = %v, want ErrInvalidCaps", err)
	}

	badOld := netsec.Ticket{Caps: invalid, ExpiresAt: now.Add(time.Hour), KeyID: "k2"}
	err := badOld.Validate(now, ring)
	if !errors.Is(err, netsec.ErrInvalidTicket) || !errors.Is(err, netsec.ErrInvalidCaps) {
		t.Fatalf("invalid ticket error = %v, want ErrInvalidTicket and ErrInvalidCaps", err)
	}
	if _, err := auth.Renew(badOld, valid, now); !errors.Is(err, netsec.ErrInvalidTicket) {
		t.Fatalf("renewing invalid ticket error = %v, want ErrInvalidTicket", err)
	}
}

func TestRenewUntilAndMissingKeyRing(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	ring := newTestRing(t, now)
	caps := mustCaps(t, "allow r pool=app")
	old := netsec.Ticket{Caps: caps, ExpiresAt: now.Add(time.Hour), KeyID: "k2"}
	auth := &netsec.Authority{Keys: ring}

	wantExpiry := now.Add(2 * time.Hour)
	renewed, err := auth.RenewUntil(old, caps, now, wantExpiry)
	if err != nil {
		t.Fatalf("RenewUntil: %v", err)
	}
	if !renewed.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("RenewUntil expiration = %s, want %s", renewed.ExpiresAt, wantExpiry)
	}

	if _, err := auth.RenewUntil(old, caps, now, now); !errors.Is(err, netsec.ErrExpired) {
		t.Fatalf("non-future renewal expiration error = %v, want ErrExpired", err)
	}
	if _, err := (&netsec.Authority{}).Renew(old, caps, now); !errors.Is(err, netsec.ErrNoKeyRing) {
		t.Fatalf("missing key ring error = %v, want ErrNoKeyRing", err)
	}
	if err := netsec.ValidateRenewal(old, caps, now, ring); err != nil {
		t.Fatalf("package-level ValidateRenewal: %v", err)
	}
}

func zeroTime() time.Time {
	return time.Time{}
}
