package qrtoken_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"example.org/netsec/q0410/qrtoken"
)

const (
	victimSession   = "browser-session-victim"
	victimDevice    = "device-fingerprint-victim"
	attackerSession = "browser-session-attacker"
	attackerDevice  = "device-fingerprint-attacker"
	mobileUser      = "user-12345"
)

// fakeClock is a manually advanced clock for deterministic expiry tests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newManager(t *testing.T, clock *fakeClock, ttl time.Duration) *qrtoken.Manager {
	t.Helper()
	return qrtoken.NewManager(
		qrtoken.WithClock(clock.Now),
		qrtoken.WithTTL(ttl),
	)
}

func issue(t *testing.T, m *qrtoken.Manager, session, device string) string {
	t.Helper()
	id, _, err := m.Issue(session, device, qrtoken.PurposeLogin)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return id
}

// TestLegitimateFlow verifies the happy path: the browser session that
// requested the QR code completes scan -> confirm -> redeem and is logged
// in as the mobile user.
func TestLegitimateFlow(t *testing.T) {
	clock := newFakeClock()
	m := newManager(t, clock, 2*time.Minute)

	id := issue(t, m, victimSession, victimDevice)

	if err := m.Scan(id); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if err := m.Confirm(id, mobileUser); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	userID, err := m.Redeem(id, victimSession, victimDevice, qrtoken.PurposeLogin)
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if userID != mobileUser {
		t.Fatalf("Redeem returned user %q, want %q", userID, mobileUser)
	}
}

// TestInterceptedTokenCrossSession verifies that an attacker who intercepts
// the token from the QR URL cannot redeem it in their own session, even
// after the legitimate user scans and confirms. The victim's original
// session still completes the login.
func TestInterceptedTokenCrossSession(t *testing.T) {
	clock := newFakeClock()
	m := newManager(t, clock, 2*time.Minute)

	id := issue(t, m, victimSession, victimDevice)

	// Victim's mobile app scans and confirms.
	if err := m.Scan(id); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if err := m.Confirm(id, mobileUser); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	// Attacker intercepted the token and tries to redeem it in their own
	// session and device.
	if _, err := m.Redeem(id, attackerSession, attackerDevice, qrtoken.PurposeLogin); !errors.Is(err, qrtoken.ErrSessionMismatch) {
		t.Fatalf("attacker redeem: got %v, want ErrSessionMismatch", err)
	}
	// Attacker with the victim's session ID but their own device.
	if _, err := m.Redeem(id, victimSession, attackerDevice, qrtoken.PurposeLogin); !errors.Is(err, qrtoken.ErrDeviceMismatch) {
		t.Fatalf("attacker redeem with stolen session ID: got %v, want ErrDeviceMismatch", err)
	}
	// Attacker replaying the token for a different purpose.
	if _, err := m.Redeem(id, victimSession, victimDevice, "payment"); !errors.Is(err, qrtoken.ErrPurposeMismatch) {
		t.Fatalf("attacker redeem with wrong purpose: got %v, want ErrPurposeMismatch", err)
	}

	// The failed attacker attempts must not consume the token: the victim's
	// original session still logs in.
	userID, err := m.Redeem(id, victimSession, victimDevice, qrtoken.PurposeLogin)
	if err != nil {
		t.Fatalf("victim redeem after attack: %v", err)
	}
	if userID != mobileUser {
		t.Fatalf("victim redeem returned user %q, want %q", userID, mobileUser)
	}
}

// TestReplay verifies every step is one-time: repeated scans, confirms, and
// redemptions are rejected.
func TestReplay(t *testing.T) {
	clock := newFakeClock()
	m := newManager(t, clock, 2*time.Minute)

	id := issue(t, m, victimSession, victimDevice)

	if err := m.Scan(id); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if err := m.Scan(id); !errors.Is(err, qrtoken.ErrAlreadyScanned) {
		t.Fatalf("second Scan: got %v, want ErrAlreadyScanned", err)
	}

	if err := m.Confirm(id, mobileUser); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if err := m.Confirm(id, mobileUser); !errors.Is(err, qrtoken.ErrAlreadyConfirmed) {
		t.Fatalf("second Confirm: got %v, want ErrAlreadyConfirmed", err)
	}
	// A confirm replay must not be able to swap the bound identity.
	if err := m.Confirm(id, "attacker-user"); !errors.Is(err, qrtoken.ErrAlreadyConfirmed) {
		t.Fatalf("Confirm with other user: got %v, want ErrAlreadyConfirmed", err)
	}

	if _, err := m.Redeem(id, victimSession, victimDevice, qrtoken.PurposeLogin); err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if _, err := m.Redeem(id, victimSession, victimDevice, qrtoken.PurposeLogin); !errors.Is(err, qrtoken.ErrAlreadyRedeemed) {
		t.Fatalf("second Redeem: got %v, want ErrAlreadyRedeemed", err)
	}
	// Scanning or confirming a consumed token is also rejected.
	if err := m.Scan(id); !errors.Is(err, qrtoken.ErrAlreadyRedeemed) {
		t.Fatalf("Scan after redeem: got %v, want ErrAlreadyRedeemed", err)
	}
	if err := m.Confirm(id, mobileUser); !errors.Is(err, qrtoken.ErrAlreadyRedeemed) {
		t.Fatalf("Confirm after redeem: got %v, want ErrAlreadyRedeemed", err)
	}
}

// TestOutOfOrder verifies the two-step handshake is enforced: confirm
// requires a prior scan, and redeem requires a prior confirm.
func TestOutOfOrder(t *testing.T) {
	clock := newFakeClock()
	m := newManager(t, clock, 2*time.Minute)

	id := issue(t, m, victimSession, victimDevice)

	if err := m.Confirm(id, mobileUser); !errors.Is(err, qrtoken.ErrNotScanned) {
		t.Fatalf("Confirm before Scan: got %v, want ErrNotScanned", err)
	}
	if _, err := m.Redeem(id, victimSession, victimDevice, qrtoken.PurposeLogin); !errors.Is(err, qrtoken.ErrNotConfirmed) {
		t.Fatalf("Redeem before Confirm: got %v, want ErrNotConfirmed", err)
	}
	if err := m.Scan(id); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if _, err := m.Redeem(id, victimSession, victimDevice, qrtoken.PurposeLogin); !errors.Is(err, qrtoken.ErrNotConfirmed) {
		t.Fatalf("Redeem after Scan only: got %v, want ErrNotConfirmed", err)
	}
}

// TestExpiry verifies tokens are rejected after their short lifetime, at
// every step of the flow.
func TestExpiry(t *testing.T) {
	clock := newFakeClock()
	m := newManager(t, clock, 30*time.Second)

	// Expired before scan.
	id1 := issue(t, m, victimSession, victimDevice)
	clock.Advance(31 * time.Second)
	if err := m.Scan(id1); !errors.Is(err, qrtoken.ErrTokenExpired) {
		t.Fatalf("Scan expired token: got %v, want ErrTokenExpired", err)
	}

	// Expires between confirm and redeem.
	id2 := issue(t, m, victimSession, victimDevice)
	if err := m.Scan(id2); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if err := m.Confirm(id2, mobileUser); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	clock.Advance(31 * time.Second)
	if _, err := m.Redeem(id2, victimSession, victimDevice, qrtoken.PurposeLogin); !errors.Is(err, qrtoken.ErrTokenExpired) {
		t.Fatalf("Redeem expired token: got %v, want ErrTokenExpired", err)
	}

	// Expired token stays dead: it does not become valid again.
	clock.Advance(-time.Minute)
	if _, err := m.Redeem(id2, victimSession, victimDevice, qrtoken.PurposeLogin); !errors.Is(err, qrtoken.ErrTokenNotFound) {
		t.Fatalf("Redeem purged token: got %v, want ErrTokenNotFound", err)
	}
}

// TestUnknownToken verifies fabricated tokens are rejected.
func TestUnknownToken(t *testing.T) {
	clock := newFakeClock()
	m := newManager(t, clock, 2*time.Minute)

	if err := m.Scan("forged-token"); !errors.Is(err, qrtoken.ErrTokenNotFound) {
		t.Fatalf("Scan forged token: got %v, want ErrTokenNotFound", err)
	}
	if _, err := m.Redeem("forged-token", victimSession, victimDevice, qrtoken.PurposeLogin); !errors.Is(err, qrtoken.ErrTokenNotFound) {
		t.Fatalf("Redeem forged token: got %v, want ErrTokenNotFound", err)
	}
}

// TestIssueValidation verifies tokens cannot be issued without the bindings
// that make redemption checks meaningful.
func TestIssueValidation(t *testing.T) {
	m := qrtoken.NewManager()

	if _, _, err := m.Issue("", victimDevice, qrtoken.PurposeLogin); err == nil {
		t.Fatal("Issue with empty session ID: want error")
	}
	if _, _, err := m.Issue(victimSession, "", qrtoken.PurposeLogin); err == nil {
		t.Fatal("Issue with empty device ID: want error")
	}
	if _, _, err := m.Issue(victimSession, victimDevice, ""); err == nil {
		t.Fatal("Issue with empty purpose: want error")
	}
}

// TestTokensAreUniqueAndOpaque verifies issued tokens are high-entropy,
// URL-safe, and never repeated.
func TestTokensAreUniqueAndOpaque(t *testing.T) {
	m := qrtoken.NewManager()
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		id, _, err := m.Issue(victimSession, victimDevice, qrtoken.PurposeLogin)
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		if len(id) < 32 {
			t.Fatalf("token too short (%d chars): %q", len(id), id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate token issued: %q", id)
		}
		seen[id] = struct{}{}
	}
}

// TestConcurrentUse exercises the manager under concurrent load to surface
// data races (run with -race).
func TestConcurrentUse(t *testing.T) {
	m := qrtoken.NewManager()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, _, err := m.Issue(victimSession, victimDevice, qrtoken.PurposeLogin)
			if err != nil {
				t.Errorf("Issue: %v", err)
				return
			}
			_ = m.Scan(id)
			_ = m.Confirm(id, mobileUser)
			_, _ = m.Redeem(id, victimSession, victimDevice, qrtoken.PurposeLogin)
		}()
	}
	wg.Wait()
}
