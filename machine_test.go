package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

// fakeClock is a fully controllable time source used in place of the
// trusted monotonic clock. Tests advance or rewind it explicitly.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func (f *fakeClock) set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t
}

func genKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv
}

func mustOpen(t *testing.T, path string, clock Clock, period time.Duration, bootstrap ...ed25519.PublicKey) *Machine {
	t.Helper()
	raw := make([][]byte, len(bootstrap))
	for i, pub := range bootstrap {
		raw[i] = []byte(pub)
	}
	m, err := Open(Config{
		Path:              path,
		Clock:             clock,
		ObservationPeriod: period,
		BootstrapKeys:     raw,
	})
	if err != nil {
		t.Fatalf("open machine: %v", err)
	}
	return m
}

func signRevoke(t *testing.T, priv ed25519.PrivateKey, signer, target KeyID) RevocationNotice {
	t.Helper()
	return RevocationNotice{
		Target:    target,
		Signer:    signer,
		Signature: ed25519.Sign(priv, RevocationMessage(target)),
	}
}

const testPeriod = 10 * time.Minute

// A new key stays a candidate until it has been observed continuously for
// the full observation period, at which point it becomes a trusted anchor.
func TestObservationPeriodPromotion(t *testing.T) {
	clock := newFakeClock()
	m := mustOpen(t, filepath.Join(t.TempDir(), "state.json"), clock, testPeriod)

	pub, _ := genKey(t)
	id := KeyIDFromPublic(pub)

	if err := m.Observe([][]byte{pub}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if got := m.Status(id); got != StatusCandidate {
		t.Fatalf("status after first sighting = %q, want candidate", got)
	}

	clock.advance(testPeriod - time.Nanosecond)
	if err := m.Observe([][]byte{pub}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if got := m.Status(id); got != StatusCandidate {
		t.Fatalf("status before period elapsed = %q, want candidate", got)
	}

	clock.advance(time.Nanosecond)
	if err := m.Observe([][]byte{pub}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if got := m.Status(id); got != StatusTrusted {
		t.Fatalf("status after period elapsed = %q, want trusted", got)
	}
}

// A single missing observation tick resets the accumulated observation
// window: a briefly seen key must wait through a fresh full period.
func TestBriefAppearanceResetsObservation(t *testing.T) {
	clock := newFakeClock()
	m := mustOpen(t, filepath.Join(t.TempDir(), "state.json"), clock, testPeriod)

	pub, _ := genKey(t)
	id := KeyIDFromPublic(pub)

	if err := m.Observe([][]byte{pub}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	firstSince, ok := m.ObservedSince(id)
	if !ok {
		t.Fatal("candidate missing after first sighting")
	}

	clock.advance(testPeriod / 2)
	if err := m.Observe(nil); err != nil {
		t.Fatalf("empty observation: %v", err)
	}
	if got := m.Status(id); got != StatusUnknown {
		t.Fatalf("status after missing tick = %q, want unknown", got)
	}

	clock.advance(testPeriod / 2)
	if err := m.Observe([][]byte{pub}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	secondSince, ok := m.ObservedSince(id)
	if !ok {
		t.Fatal("candidate not re-registered after reappearance")
	}
	if !secondSince.After(firstSince) {
		t.Fatalf("observation window was not reset: %s not after %s", secondSince, firstSince)
	}

	// Only half a period since the reset, so it must still be a candidate.
	clock.advance(testPeriod / 2)
	if err := m.Observe([][]byte{pub}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if got := m.Status(id); got != StatusCandidate {
		t.Fatalf("status before fresh period elapsed = %q, want candidate", got)
	}

	clock.advance(testPeriod / 2)
	if err := m.Observe([][]byte{pub}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if got := m.Status(id); got != StatusTrusted {
		t.Fatalf("status after fresh period = %q, want trusted", got)
	}
}

// The observation window survives a process restart because all timing
// state is persisted against the trusted clock.
func TestRestartPreservesObservationWindow(t *testing.T) {
	clock := newFakeClock()
	path := filepath.Join(t.TempDir(), "state.json")

	pub, _ := genKey(t)
	id := KeyIDFromPublic(pub)

	m := mustOpen(t, path, clock, testPeriod)
	if err := m.Observe([][]byte{pub}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	clock.advance(testPeriod / 2)
	if err := m.Observe([][]byte{pub}); err != nil {
		t.Fatalf("observe: %v", err)
	}

	// Simulate a restart: reopen the same state file with the same clock.
	reopened := mustOpen(t, path, clock, testPeriod)
	if got := reopened.Status(id); got != StatusCandidate {
		t.Fatalf("status after restart = %q, want candidate", got)
	}
	since, ok := reopened.ObservedSince(id)
	if !ok {
		t.Fatal("observation window lost across restart")
	}

	clock.advance(testPeriod / 2)
	if err := reopened.Observe([][]byte{pub}); err != nil {
		t.Fatalf("observe after restart: %v", err)
	}
	if got := reopened.Status(id); got != StatusTrusted {
		t.Fatalf("status after resumed window = %q, want trusted", got)
	}
	if since.IsZero() {
		t.Fatal("persisted observation start time is zero")
	}
}

// A backwards jump of the trusted clock is rejected, both within a
// process and across a restart where the fake clock is rewound below the
// persisted high-water mark.
func TestClockRollbackRejected(t *testing.T) {
	clock := newFakeClock()
	path := filepath.Join(t.TempDir(), "state.json")

	pub, _ := genKey(t)
	id := KeyIDFromPublic(pub)

	m := mustOpen(t, path, clock, testPeriod)
	if err := m.Observe([][]byte{pub}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	clock.advance(time.Minute)
	// A tick at the advanced time fixes the high-water mark on disk.
	if err := m.Observe([][]byte{pub}); err != nil {
		t.Fatalf("observe before rollback: %v", err)
	}
	highWater := clock.Now()

	clock.advance(-time.Second)
	if err := m.Observe([][]byte{pub}); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("in-process rollback err = %v, want ErrClockRollback", err)
	}

	// Restart with a clock rewound below the persisted watermark.
	rewound := newFakeClock()
	rewound.set(highWater.Add(-time.Minute))
	reopened := mustOpen(t, path, rewound, testPeriod)
	if err := reopened.Observe([][]byte{pub}); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("post-restart rollback err = %v, want ErrClockRollback", err)
	}
	if err := reopened.Revoke(RevocationNotice{Target: id, Signer: id, Signature: []byte("x")}); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("revoke rollback err = %v, want ErrClockRollback", err)
	}

	// Advancing back to a valid instant restores normal operation.
	rewound.set(highWater)
	if err := reopened.Observe([][]byte{pub}); err != nil {
		t.Fatalf("observe after clock restored: %v", err)
	}
}

// A newly promoted anchor coexists with the original trusted anchor;
// neither replaces the other until an explicit revocation.
func TestTwoAnchorsCoexist(t *testing.T) {
	clock := newFakeClock()
	path := filepath.Join(t.TempDir(), "state.json")

	oldPub, _ := genKey(t)
	newPub, _ := genKey(t)
	oldID := KeyIDFromPublic(oldPub)
	newID := KeyIDFromPublic(newPub)

	m := mustOpen(t, path, clock, testPeriod, oldPub)
	if got := m.Status(oldID); got != StatusTrusted {
		t.Fatalf("bootstrap anchor status = %q, want trusted", got)
	}

	if err := m.Observe([][]byte{oldPub, newPub}); err != nil {
		t.Fatalf("observe both anchors: %v", err)
	}
	clock.advance(testPeriod)
	if err := m.Observe([][]byte{oldPub, newPub}); err != nil {
		t.Fatalf("observe both anchors: %v", err)
	}

	trusted := m.Trusted()
	if len(trusted) != 2 {
		t.Fatalf("trusted count = %d, want 2: %v", len(trusted), trusted)
	}
	wantIDs := []KeyID{oldID, newID}
	slices.Sort(wantIDs)
	if !slices.Equal(trusted, wantIDs) {
		t.Fatalf("trusted = %v, want %v", trusted, wantIDs)
	}

	// The old anchor remains trusted even when the new one is absent;
	// trusted anchors never expire on their own.
	if err := m.Observe([][]byte{oldPub}); err != nil {
		t.Fatalf("observe old anchor only: %v", err)
	}
	if m.Status(oldID) != StatusTrusted || m.Status(newID) != StatusTrusted {
		t.Fatalf("trusted anchors must not expire: old=%q new=%q", m.Status(oldID), m.Status(newID))
	}
}

// Revocation flags are valid only when signed by a currently trusted
// anchor, and revoked keys are tombstoned across restarts.
func TestRevocationByTrustedAnchor(t *testing.T) {
	clock := newFakeClock()
	path := filepath.Join(t.TempDir(), "state.json")

	oldPub, oldPriv := genKey(t)
	newPub, _ := genKey(t)
	oldID := KeyIDFromPublic(oldPub)
	newID := KeyIDFromPublic(newPub)

	m := mustOpen(t, path, clock, testPeriod, oldPub)
	if err := m.Observe([][]byte{oldPub, newPub}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	clock.advance(testPeriod)
	if err := m.Observe([][]byte{oldPub, newPub}); err != nil {
		t.Fatalf("observe: %v", err)
	}

	// A tampered signature must fail verification.
	good := signRevoke(t, oldPriv, oldID, newID)
	tampered := good
	tampered.Signature = append([]byte(nil), good.Signature...)
	tampered.Signature[0] ^= 0xFF
	if err := m.Revoke(tampered); !errors.Is(err, ErrInvalidNotice) {
		t.Fatalf("tampered revocation err = %v, want ErrInvalidNotice", err)
	}

	// A valid revocation signed by the original trusted anchor succeeds.
	if err := m.Revoke(good); err != nil {
		t.Fatalf("valid revocation: %v", err)
	}
	if got := m.Status(newID); got != StatusRevoked {
		t.Fatalf("revoked anchor status = %q, want revoked", got)
	}
	if got := m.Status(oldID); got != StatusTrusted {
		t.Fatalf("signer anchor status = %q, want trusted", got)
	}

	// Re-revoking is rejected, and a revoked key can never re-enter.
	if err := m.Revoke(good); !errors.Is(err, ErrRevoked) {
		t.Fatalf("repeat revocation err = %v, want ErrRevoked", err)
	}
	if err := m.Observe([][]byte{oldPub, newPub}); err != nil {
		t.Fatalf("observe after revocation: %v", err)
	}
	if got := m.Status(newID); got != StatusRevoked {
		t.Fatalf("revoked anchor re-entered as %q", got)
	}

	// The tombstone persists across a restart.
	reopened := mustOpen(t, path, clock, testPeriod)
	if got := reopened.Status(newID); got != StatusRevoked {
		t.Fatalf("status after restart = %q, want revoked", got)
	}
}

// Candidate keys cannot authorize revocations: only currently trusted
// anchors are acceptable signers.
func TestCandidateCannotRevoke(t *testing.T) {
	clock := newFakeClock()
	path := filepath.Join(t.TempDir(), "state.json")

	oldPub, oldPriv := genKey(t)
	oldID := KeyIDFromPublic(oldPub)
	candidatePub, candidatePriv := genKey(t)
	candidateID := KeyIDFromPublic(candidatePub)

	m := mustOpen(t, path, clock, testPeriod, oldPub)
	if err := m.Observe([][]byte{oldPub, candidatePub}); err != nil {
		t.Fatalf("observe: %v", err)
	}

	notice := signRevoke(t, candidatePriv, candidateID, oldID)
	if err := m.Revoke(notice); !errors.Is(err, ErrUntrustedSigner) {
		t.Fatalf("candidate-signed revocation err = %v, want ErrUntrustedSigner", err)
	}
	if got := m.Status(oldID); got != StatusTrusted {
		t.Fatalf("target status = %q, want still trusted", got)
	}

	unknownPub, _ := genKey(t)
	unknownID := KeyIDFromPublic(unknownPub)
	if err := m.Revoke(signRevoke(t, oldPriv, oldID, unknownID)); !errors.Is(err, ErrUnknownAnchor) {
		t.Fatalf("unknown target revocation err = %v, want ErrUnknownAnchor", err)
	}
}

// Malformed keys and configuration are rejected rather than stored.
func TestInvalidInputs(t *testing.T) {
	clock := newFakeClock()
	dir := t.TempDir()

	if _, err := Open(Config{Path: filepath.Join(dir, "a.json"), Clock: clock}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("zero period err = %v, want ErrInvalidConfig", err)
	}
	if _, err := Open(Config{Path: "", Clock: clock, ObservationPeriod: testPeriod}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty path err = %v, want ErrInvalidConfig", err)
	}

	m := mustOpen(t, filepath.Join(dir, "b.json"), clock, testPeriod)
	if err := m.Observe([][]byte{make([]byte, ed25519.PublicKeySize-1)}); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("short key err = %v, want ErrInvalidKey", err)
	}
	if err := m.Revoke(RevocationNotice{}); !errors.Is(err, ErrInvalidNotice) {
		t.Fatalf("empty notice err = %v, want ErrInvalidNotice", err)
	}
}

// A candidate's reset due to a missing tick must persist: after a restart
// the key starts a brand-new observation window.
func TestMissingTickResetPersistsAcrossRestart(t *testing.T) {
	clock := newFakeClock()
	path := filepath.Join(t.TempDir(), "state.json")

	pub, _ := genKey(t)
	id := KeyIDFromPublic(pub)

	m := mustOpen(t, path, clock, testPeriod)
	if err := m.Observe([][]byte{pub}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	clock.advance(testPeriod / 2)
	if err := m.Observe(nil); err != nil {
		t.Fatalf("empty observation: %v", err)
	}

	reopened := mustOpen(t, path, clock, testPeriod)
	if got := reopened.Status(id); got != StatusUnknown {
		t.Fatalf("status after restart = %q, want unknown (reset persisted)", got)
	}

	if err := reopened.Observe([][]byte{pub}); err != nil {
		t.Fatalf("observe after restart: %v", err)
	}
	clock.advance(testPeriod - time.Nanosecond)
	if err := reopened.Observe([][]byte{pub}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if got := reopened.Status(id); got != StatusCandidate {
		t.Fatalf("status nearly one period after fresh window = %q, want candidate", got)
	}
}

// A restarted machine whose clock sits below the watermark must not
// promote candidates even when the configured period appears to have
// elapsed, because no trusted tick can be recorded.
func TestRollbackBlocksPromotionAfterRestart(t *testing.T) {
	clock := newFakeClock()
	path := filepath.Join(t.TempDir(), "state.json")

	pub, _ := genKey(t)
	id := KeyIDFromPublic(pub)

	m := mustOpen(t, path, clock, testPeriod)
	if err := m.Observe([][]byte{pub}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	clock.advance(testPeriod / 2)
	if err := m.Observe([][]byte{pub}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	highWater := clock.Now()

	rewound := newFakeClock()
	rewound.set(highWater.Add(-time.Second))
	reopened := mustOpen(t, path, rewound, testPeriod)
	if err := reopened.Observe([][]byte{pub}); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("rollback tick err = %v, want ErrClockRollback", err)
	}
	if got := reopened.Status(id); got != StatusCandidate {
		t.Fatalf("status after rejected tick = %q, want still candidate (no promotion)", got)
	}
}

// A revoked key observed again after restart stays tombstoned and is
// never re-registered as a candidate.
func TestRevokedKeyNeverReenters(t *testing.T) {
	clock := newFakeClock()
	path := filepath.Join(t.TempDir(), "state.json")

	oldPub, oldPriv := genKey(t)
	oldID := KeyIDFromPublic(oldPub)

	m := mustOpen(t, path, clock, testPeriod, oldPub)
	if err := m.Revoke(signRevoke(t, oldPriv, oldID, oldID)); err != nil {
		t.Fatalf("self-revoke: %v", err)
	}
	if got := m.Status(oldID); got != StatusRevoked {
		t.Fatalf("self-revoked status = %q, want revoked", got)
	}

	reopened := mustOpen(t, path, clock, testPeriod)
	for range 3 {
		clock.advance(testPeriod)
		if err := reopened.Observe([][]byte{oldPub}); err != nil {
			t.Fatalf("observe revoked key: %v", err)
		}
	}
	if got := reopened.Status(oldID); got != StatusRevoked {
		t.Fatalf("revoked key re-entered as %q", got)
	}
	if _, ok := reopened.ObservedSince(oldID); ok {
		t.Fatal("revoked key must never open an observation window")
	}
}
