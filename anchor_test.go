package netsec

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testEpoch int64 = 1_700_000_000_000_000_000

func testKey(t *testing.T, name string) (ed25519.PrivateKey, Anchor) {
	t.Helper()
	sum := sha256.Sum256([]byte("netsec-test-key:" + name))
	privateKey := ed25519.NewKeyFromSeed(sum[:])
	publicKey := privateKey.Public().(ed25519.PublicKey)
	return privateKey, Anchor{Name: name, PublicKey: publicKey}
}

func signRevocation(privateKey ed25519.PrivateKey, target KeyID) []byte {
	return ed25519.Sign(privateKey, RevocationPayload(target))
}

func openTestMachine(t *testing.T, store Store, clock Clock, period time.Duration,
	initial ...Anchor,
) *Machine {
	t.Helper()
	machine, err := Open(Config{
		ObservationPeriod: period,
		Clock:             clock,
		Store:             store,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !machine.Initialized() {
		if err := machine.Bootstrap(initial); err != nil {
			t.Fatalf("Bootstrap: %v", err)
		}
	}
	return machine
}

func activeKeys(snapshot Snapshot) map[KeyID]bool {
	active := make(map[KeyID]bool, len(snapshot.Active))
	for _, anchor := range snapshot.Active {
		active[anchor.KeyID()] = true
	}
	return active
}

func requireActive(t *testing.T, snapshot Snapshot, anchors ...Anchor) {
	t.Helper()
	active := activeKeys(snapshot)
	if len(active) != len(anchors) {
		t.Fatalf("active anchor count = %d, want %d", len(active), len(anchors))
	}
	for _, anchor := range anchors {
		if !active[anchor.KeyID()] {
			t.Fatalf("anchor %s is not active", anchor.KeyID())
		}
	}
}

func requireNoPending(t *testing.T, snapshot Snapshot) {
	t.Helper()
	if len(snapshot.Pending) != 0 {
		t.Fatalf("pending anchors = %d, want 0", len(snapshot.Pending))
	}
}

func TestNewAnchorNeedsFullObservationPeriodAndCoexists(t *testing.T) {
	const period = 10 * time.Minute
	clock := NewManualClockAt(testEpoch)
	store := NewMemoryStore()
	_, oldAnchor := testKey(t, "old")
	_, newAnchor := testKey(t, "new")
	machine := openTestMachine(t, store, clock, period, oldAnchor)

	start := clock.Now()
	result, err := machine.Reconcile([]Anchor{oldAnchor, newAnchor})
	if err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	if len(result.Promoted) != 0 {
		t.Fatalf("new anchor promoted immediately")
	}
	if len(result.Pending) != 1 || result.Pending[0].Since != start {
		t.Fatalf("pending = %+v, want one starting at %d", result.Pending, start)
	}
	if result.Pending[0].Remaining != period {
		t.Fatalf("remaining = %s, want %s", result.Pending[0].Remaining, period)
	}
	requireActive(t, machine.Snapshot(), oldAnchor)

	clock.Advance(period - time.Nanosecond)
	result, err = machine.Reconcile([]Anchor{oldAnchor, newAnchor})
	if err != nil {
		t.Fatalf("Reconcile before deadline: %v", err)
	}
	if len(result.Promoted) != 0 {
		t.Fatalf("new anchor promoted before deadline")
	}
	if len(result.Pending) != 1 || result.Pending[0].Remaining != time.Nanosecond {
		t.Fatalf("pending before deadline = %+v", result.Pending)
	}

	clock.Advance(time.Nanosecond)
	result, err = machine.Reconcile([]Anchor{oldAnchor, newAnchor})
	if err != nil {
		t.Fatalf("Reconcile at deadline: %v", err)
	}
	if len(result.Promoted) != 1 || result.Promoted[0].KeyID() != newAnchor.KeyID() {
		t.Fatalf("promoted = %+v, want new anchor", result.Promoted)
	}
	snapshot := machine.Snapshot()
	requireActive(t, snapshot, oldAnchor, newAnchor)
	requireNoPending(t, snapshot)

}

func TestMissingObservationResetsContinuousWindow(t *testing.T) {
	const period = 10 * time.Minute
	clock := NewManualClockAt(testEpoch)
	store := NewMemoryStore()
	_, oldAnchor := testKey(t, "old")
	_, newAnchor := testKey(t, "new")
	machine := openTestMachine(t, store, clock, period, oldAnchor)

	firstSeen := clock.Now()
	if _, err := machine.Reconcile([]Anchor{oldAnchor, newAnchor}); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	clock.Advance(6 * time.Minute)
	if _, err := machine.Reconcile([]Anchor{oldAnchor, newAnchor}); err != nil {
		t.Fatalf("continuous Reconcile: %v", err)
	}
	pending := machine.Snapshot().Pending
	if len(pending) != 1 || pending[0].Since != firstSeen {
		t.Fatalf("pending after continuous observation = %+v, want since %d", pending, firstSeen)
	}

	clock.Advance(time.Minute)
	result, err := machine.Reconcile([]Anchor{oldAnchor})
	if err != nil {
		t.Fatalf("Reconcile with missing key: %v", err)
	}
	if len(result.Reset) != 1 || result.Reset[0] != newAnchor.KeyID() {
		t.Fatalf("reset = %v, want %s", result.Reset, newAnchor.KeyID())
	}
	requireNoPending(t, machine.Snapshot())

	clock.Advance(time.Minute)
	secondSeen := clock.Now()
	if _, err := machine.Reconcile([]Anchor{oldAnchor, newAnchor}); err != nil {
		t.Fatalf("Reconcile after reappearance: %v", err)
	}
	pending = machine.Snapshot().Pending
	if len(pending) != 1 || pending[0].Since != secondSeen {
		t.Fatalf("pending after reappearance = %+v, want since %d", pending, secondSeen)
	}

	clock.Advance(period - time.Nanosecond)
	result, err = machine.Reconcile([]Anchor{oldAnchor, newAnchor})
	if err != nil {
		t.Fatalf("Reconcile near deadline: %v", err)
	}
	if len(result.Promoted) != 0 {
		t.Fatalf("new anchor promoted using pre-missing observation time")
	}

	clock.Advance(time.Nanosecond)
	result, err = machine.Reconcile([]Anchor{oldAnchor, newAnchor})
	if err != nil {
		t.Fatalf("Reconcile at new deadline: %v", err)
	}
	if len(result.Promoted) != 1 {
		t.Fatalf("new anchor not promoted after full continuous period")
	}
	requireActive(t, machine.Snapshot(), oldAnchor, newAnchor)
}

func TestRestartPersistsPendingObservationAndDeadline(t *testing.T) {
	const period = 10 * time.Minute
	store := NewMemoryStore()
	clockBeforeRestart := NewManualClockAt(testEpoch)
	_, oldAnchor := testKey(t, "old")
	_, newAnchor := testKey(t, "new")

	before := openTestMachine(t, store, clockBeforeRestart, period, oldAnchor)
	firstSeen := clockBeforeRestart.Now()
	if _, err := before.Reconcile([]Anchor{oldAnchor, newAnchor}); err != nil {
		t.Fatalf("Reconcile before restart: %v", err)
	}
	clockBeforeRestart.Advance(4 * time.Minute)
	if _, err := before.Reconcile([]Anchor{oldAnchor, newAnchor}); err != nil {
		t.Fatalf("Reconcile before restart: %v", err)
	}

	clockAfterRestart := NewManualClockAt(clockBeforeRestart.Now())
	after, err := Open(Config{
		ObservationPeriod: period,
		Clock:             clockAfterRestart,
		Store:             store,
	})
	if err != nil {
		t.Fatalf("Open after restart: %v", err)
	}
	snapshot := after.Snapshot()
	if snapshot.LastTick != clockBeforeRestart.Now() {
		t.Fatalf("last tick after restart = %d, want %d", snapshot.LastTick, clockBeforeRestart.Now())
	}
	if len(snapshot.Pending) != 1 || snapshot.Pending[0].Since != firstSeen {
		t.Fatalf("pending after restart = %+v, want since %d", snapshot.Pending, firstSeen)
	}
	if snapshot.Pending[0].Remaining != 6*time.Minute {
		t.Fatalf("remaining after restart = %s, want 6m", snapshot.Pending[0].Remaining)
	}

	result, err := after.Reconcile([]Anchor{oldAnchor, newAnchor})
	if err != nil {
		t.Fatalf("Reconcile after restart: %v", err)
	}
	if len(result.Promoted) != 0 {
		t.Fatalf("new anchor promoted immediately after restart")
	}

	clockAfterRestart.Advance(6 * time.Minute)
	result, err = after.Reconcile([]Anchor{oldAnchor, newAnchor})
	if err != nil {
		t.Fatalf("Reconcile at persisted deadline: %v", err)
	}
	if len(result.Promoted) != 1 {
		t.Fatalf("new anchor not promoted at persisted deadline")
	}
	requireActive(t, after.Snapshot(), oldAnchor, newAnchor)
}

func TestClockRollbackAfterRestartCannotShortenObservation(t *testing.T) {
	const period = 10 * time.Minute
	store := NewMemoryStore()
	clockBeforeRestart := NewManualClockAt(testEpoch)
	_, oldAnchor := testKey(t, "old")
	_, newAnchor := testKey(t, "new")

	before := openTestMachine(t, store, clockBeforeRestart, period, oldAnchor)
	if _, err := before.Reconcile([]Anchor{oldAnchor, newAnchor}); err != nil {
		t.Fatalf("Reconcile before restart: %v", err)
	}
	clockBeforeRestart.Advance(9 * time.Minute)
	if _, err := before.Reconcile([]Anchor{oldAnchor, newAnchor}); err != nil {
		t.Fatalf("Reconcile before restart: %v", err)
	}

	rolledBack := NewManualClockAt(testEpoch + int64(time.Minute))
	after, err := Open(Config{
		ObservationPeriod: period,
		Clock:             rolledBack,
		Store:             store,
	})
	if err != nil {
		t.Fatalf("Open after rollback: %v", err)
	}
	result, err := after.Reconcile([]Anchor{oldAnchor, newAnchor})
	if err != nil {
		t.Fatalf("Reconcile after rollback: %v", err)
	}
	if !result.ClockRegressed {
		t.Fatalf("clock rollback was not reported")
	}
	if len(result.Promoted) != 0 {
		t.Fatalf("new anchor promoted after clock rollback")
	}
	if snapshot := after.Snapshot(); snapshot.LastTick != testEpoch+int64(9*time.Minute) {
		t.Fatalf("last tick regressed to %d", snapshot.LastTick)
	}

	rolledBack.Advance(8 * time.Minute)
	result, err = after.Reconcile([]Anchor{oldAnchor, newAnchor})
	if err != nil {
		t.Fatalf("Reconcile while catching up: %v", err)
	}
	if len(result.Promoted) != 0 {
		t.Fatalf("new anchor promoted before time caught up")
	}

	rolledBack.Advance(time.Minute)
	result, err = after.Reconcile([]Anchor{oldAnchor, newAnchor})
	if err != nil {
		t.Fatalf("Reconcile after catch-up: %v", err)
	}
	if len(result.Promoted) != 1 {
		t.Fatalf("new anchor not promoted after catch-up")
	}
}

func TestRevocationRequiresActiveAnchorSignatureAndIsPermanent(t *testing.T) {
	const period = time.Minute
	clock := NewManualClockAt(testEpoch)
	store := NewMemoryStore()
	oldKey, oldAnchor := testKey(t, "old")
	newKey, newAnchor := testKey(t, "new")
	attackerKey, _ := testKey(t, "attacker")
	machine := openTestMachine(t, store, clock, period, oldAnchor)

	if _, err := machine.Reconcile([]Anchor{oldAnchor, newAnchor}); err != nil {
		t.Fatalf("Reconcile new key: %v", err)
	}
	if _, err := machine.Revoke(oldAnchor.KeyID(), signRevocation(newKey, oldAnchor.KeyID())); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("candidate-signed revoke error = %v, want ErrInvalidSignature", err)
	}
	if _, err := machine.Revoke(newAnchor.KeyID(), signRevocation(attackerKey, newAnchor.KeyID())); !errors.Is(err, ErrUnknownAnchor) {
		t.Fatalf("revoke candidate error = %v, want ErrUnknownAnchor", err)
	}

	clock.Advance(period)
	if _, err := machine.Reconcile([]Anchor{oldAnchor, newAnchor}); err != nil {
		t.Fatalf("promote new key: %v", err)
	}
	requireActive(t, machine.Snapshot(), oldAnchor, newAnchor)

	if _, err := machine.Revoke(newAnchor.KeyID(), signRevocation(attackerKey, newAnchor.KeyID())); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("untrusted revoke error = %v, want ErrInvalidSignature", err)
	}
	requireActive(t, machine.Snapshot(), oldAnchor, newAnchor)

	result, err := machine.Revoke(newAnchor.KeyID(), signRevocation(oldKey, newAnchor.KeyID()))
	if err != nil {
		t.Fatalf("trusted Revoke: %v", err)
	}
	if result.VerifiedBy != oldAnchor.KeyID() {
		t.Fatalf("revocation signer = %s, want %s", result.VerifiedBy, oldAnchor.KeyID())
	}
	requireActive(t, machine.Snapshot(), oldAnchor)

	clock.Advance(2 * period)
	reconcileResult, err := machine.Reconcile([]Anchor{oldAnchor, newAnchor})
	if err != nil {
		t.Fatalf("Reconcile revoked key: %v", err)
	}
	if len(reconcileResult.Promoted) != 0 || len(reconcileResult.Pending) != 0 {
		t.Fatalf("revoked key became a candidate again: %+v", reconcileResult)
	}
	if len(reconcileResult.IgnoredRevoked) != 1 ||
		reconcileResult.IgnoredRevoked[0] != newAnchor.KeyID() {
		t.Fatalf("ignored revoked = %v", reconcileResult.IgnoredRevoked)
	}

	if _, err := machine.Revoke(oldAnchor.KeyID(), signRevocation(oldKey, oldAnchor.KeyID())); !errors.Is(err, ErrLastAnchor) {
		t.Fatalf("last-anchor revoke error = %v, want ErrLastAnchor", err)
	}

	restarted, err := Open(Config{
		ObservationPeriod: period,
		Clock:             NewManualClockAt(clock.Now()),
		Store:             store,
	})
	if err != nil {
		t.Fatalf("Open after revocation: %v", err)
	}
	if revoked := restarted.Snapshot().Revocations; len(revoked) != 1 ||
		revoked[0].Target != newAnchor.KeyID() {
		t.Fatalf("persisted revocations = %+v", revoked)
	}
	reconcileResult, err = restarted.Reconcile([]Anchor{oldAnchor, newAnchor})
	if err != nil {
		t.Fatalf("Reconcile after restart: %v", err)
	}
	if len(reconcileResult.IgnoredRevoked) != 1 ||
		reconcileResult.IgnoredRevoked[0] != newAnchor.KeyID() {
		t.Fatalf("persisted tombstone was not enforced: %+v", reconcileResult)
	}
}

func TestFileStoreSurvivesRestartAndUsesPrivatePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "anchors.json")
	store := NewFileStore(path)
	clock := NewManualClockAt(testEpoch)
	_, oldAnchor := testKey(t, "old")

	machine := openTestMachine(t, store, clock, time.Hour, oldAnchor)
	if err := machine.Bootstrap([]Anchor{oldAnchor}); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("second Bootstrap error = %v, want ErrAlreadyInitialized", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("state permissions = %o, want 600", mode)
	}

	restarted, err := Open(Config{
		ObservationPeriod: time.Hour,
		Clock:             NewManualClockAt(clock.Now()),
		Store:             NewFileStore(path),
	})
	if err != nil {
		t.Fatalf("Open from file: %v", err)
	}
	requireActive(t, restarted.Snapshot(), oldAnchor)
}

func TestObservationPeriodCannotChangeDownwardOnRestart(t *testing.T) {
	store := NewMemoryStore()
	clock := NewManualClockAt(testEpoch)
	_, oldAnchor := testKey(t, "old")
	openTestMachine(t, store, clock, 10*time.Minute, oldAnchor)

	_, err := Open(Config{
		ObservationPeriod: time.Minute,
		Clock:             NewManualClockAt(clock.Now()),
		Store:             store,
	})
	if !errors.Is(err, ErrConfigMismatch) {
		t.Fatalf("Open with shortened period error = %v, want ErrConfigMismatch", err)
	}
}
