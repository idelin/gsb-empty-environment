package txnlease

import (
	"errors"
	"math/rand/v2"
	"net/netip"
	"testing"
	"time"
)

const testIsolation = 30 * time.Second

var (
	testEndpoint = netip.MustParseAddrPort("203.0.113.53:53")
	testQuestion = Question{Name: "example.com.", Type: 1, Class: 1}
)

func newTestManager(t *testing.T, space int) (*Manager, *ManualClock) {
	t.Helper()
	clock := NewManualClock(time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC))
	mgr, err := NewManager(Config{
		Space:     space,
		Isolation: testIsolation,
		Clock:     clock,
		Rand:      rand.New(rand.NewPCG(1, 2)),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return mgr, clock
}

func mustAcquire(t *testing.T, mgr *Manager, conn ConnID) *Lease {
	t.Helper()
	l, err := mgr.Acquire(testEndpoint, conn, testQuestion)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	return l
}

func mustChallenge(t *testing.T, mgr *Manager, l *Lease) uint64 {
	t.Helper()
	c, ok := mgr.Challenge(l)
	if !ok {
		t.Fatalf("Challenge: lease %d not active", l.ID())
	}
	return c
}

func responseFor(t *testing.T, mgr *Manager, l *Lease, conn ConnID) Response {
	t.Helper()
	return Response{
		Endpoint:  testEndpoint,
		Conn:      conn,
		ID:        l.ID(),
		Question:  testQuestion,
		Challenge: mustChallenge(t, mgr, l),
	}
}

func mustVerdict(t *testing.T, mgr *Manager, r Response, want Verdict) {
	t.Helper()
	got, err := mgr.Deliver(r)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if got != want {
		t.Fatalf("Deliver verdict = %v, want %v", got, want)
	}
}

func TestDeliverHappyPath(t *testing.T) {
	mgr, _ := newTestManager(t, 2)
	l := mustAcquire(t, mgr, 1)
	mustVerdict(t, mgr, responseFor(t, mgr, l, 1), Delivered)
	if st := mgr.Stats(); st.Isolation != 1 || st.Free != 1 {
		t.Fatalf("Stats = %+v, want 1 isolation 1 free", st)
	}
}

func TestDuplicateResponseDropped(t *testing.T) {
	mgr, clock := newTestManager(t, 2)
	l := mustAcquire(t, mgr, 1)
	resp := responseFor(t, mgr, l, 1)
	mustVerdict(t, mgr, resp, Delivered)
	// A retransmitted duplicate inside the isolation window is stale.
	clock.Advance(time.Second)
	mustVerdict(t, mgr, resp, Stale)
	// Still stale near the end of the window.
	clock.Advance(testIsolation - 2*time.Second)
	mustVerdict(t, mgr, resp, Stale)
}

func TestForgedQuestionRejected(t *testing.T) {
	mgr, _ := newTestManager(t, 2)
	l := mustAcquire(t, mgr, 1)
	resp := responseFor(t, mgr, l, 1)

	for _, q := range []Question{
		{Name: "attacker.example.", Type: 1, Class: 1},
		{Name: "example.com.", Type: 28, Class: 1},
		{Name: "example.com.", Type: 1, Class: 3},
	} {
		forged := resp
		forged.Question = q
		mustVerdict(t, mgr, forged, Mismatch)
	}
	// The genuine lease survives the forgery attempts.
	mustVerdict(t, mgr, resp, Delivered)
}

func TestForgedChallengeAndEndpointRejected(t *testing.T) {
	mgr, _ := newTestManager(t, 2)
	l := mustAcquire(t, mgr, 1)
	resp := responseFor(t, mgr, l, 1)

	badChallenge := resp
	badChallenge.Challenge++
	mustVerdict(t, mgr, badChallenge, Mismatch)

	badEndpoint := resp
	badEndpoint.Endpoint = netip.MustParseAddrPort("198.51.100.99:53")
	mustVerdict(t, mgr, badEndpoint, Mismatch)

	mustVerdict(t, mgr, resp, Delivered)
}

func TestIsolationWindowBlocksReuse(t *testing.T) {
	mgr, clock := newTestManager(t, 1)
	l := mustAcquire(t, mgr, 1)
	mustVerdict(t, mgr, responseFor(t, mgr, l, 1), Delivered)

	if _, err := mgr.Acquire(testEndpoint, 1, testQuestion); !errors.Is(err, ErrExhausted) {
		t.Fatalf("Acquire during isolation = %v, want ErrExhausted", err)
	}
	clock.Advance(testIsolation - time.Second)
	if _, err := mgr.Acquire(testEndpoint, 1, testQuestion); !errors.Is(err, ErrExhausted) {
		t.Fatalf("Acquire before window end = %v, want ErrExhausted", err)
	}
	clock.Advance(time.Second)
	l2 := mustAcquire(t, mgr, 1)
	if l2.ID() != l.ID() {
		t.Fatalf("reused ID = %d, want %d", l2.ID(), l.ID())
	}
}

func TestCancelHoldsSlotAgainstLateResponse(t *testing.T) {
	mgr, clock := newTestManager(t, 1)
	l := mustAcquire(t, mgr, 1)
	resp := responseFor(t, mgr, l, 1)

	if !l.Cancel() {
		t.Fatal("Cancel of active lease = false")
	}
	if l.Cancel() {
		t.Fatal("second Cancel = true, want false")
	}
	// The cancelled slot is quarantined, not recycled.
	if _, err := mgr.Acquire(testEndpoint, 1, testQuestion); !errors.Is(err, ErrExhausted) {
		t.Fatalf("Acquire after cancel = %v, want ErrExhausted", err)
	}
	// The in-flight response arrives after cancellation: dropped as stale.
	clock.Advance(5 * time.Second)
	mustVerdict(t, mgr, resp, Stale)
	// Only after the window may the ID be reused.
	clock.Advance(testIsolation)
	mustAcquire(t, mgr, 1)
}

func TestLatePacketAfterReconnect(t *testing.T) {
	mgr, clock := newTestManager(t, 1)
	var conns ConnIDSource

	conn1 := conns.Next()
	l1 := mustAcquire(t, mgr, conn1)
	oldResp := responseFor(t, mgr, l1, conn1)

	// Connection dies; the forwarder cancels outstanding queries and
	// reconnects, obtaining a new connection instance.
	l1.Cancel()
	conn2 := conns.Next()
	if conn2 == conn1 {
		t.Fatal("ConnIDSource reused an instance ID")
	}
	clock.Advance(testIsolation)
	l2 := mustAcquire(t, mgr, conn2)

	// A late packet from the dead connection carries the old instance ID
	// and must not be delivered to the new query on the same slot.
	mustVerdict(t, mgr, oldResp, Mismatch)
	if st := mgr.Stats(); st.Active != 1 {
		t.Fatalf("Stats = %+v, want the new lease still active", st)
	}
	// The genuine response on the new connection still works.
	mustVerdict(t, mgr, responseFor(t, mgr, l2, conn2), Delivered)
}

func TestExhaustion(t *testing.T) {
	mgr, _ := newTestManager(t, 2)
	mustAcquire(t, mgr, 1)
	mustAcquire(t, mgr, 1)
	if _, err := mgr.Acquire(testEndpoint, 1, testQuestion); !errors.Is(err, ErrExhausted) {
		t.Fatalf("Acquire with full space = %v, want ErrExhausted", err)
	}
}

func TestStaleLeaseHandleCannotAct(t *testing.T) {
	mgr, clock := newTestManager(t, 1)
	l1 := mustAcquire(t, mgr, 1)
	mustVerdict(t, mgr, responseFor(t, mgr, l1, 1), Delivered)

	clock.Advance(testIsolation)
	l2 := mustAcquire(t, mgr, 1)

	// The old handle must not cancel the new tenant of the same slot.
	if l1.Cancel() {
		t.Fatal("stale lease Cancel = true, want false")
	}
	if _, ok := mgr.Challenge(l1); ok {
		t.Fatal("stale lease Challenge ok = true, want false")
	}
	mustVerdict(t, mgr, responseFor(t, mgr, l2, 1), Delivered)
}

func TestChallengesAreDistinct(t *testing.T) {
	mgr, _ := newTestManager(t, 4)
	seen := map[uint64]bool{}
	for i := 0; i < 4; i++ {
		l := mustAcquire(t, mgr, 1)
		c := mustChallenge(t, mgr, l)
		if seen[c] {
			t.Fatalf("challenge %d reused within the window", c)
		}
		seen[c] = true
	}
}

func TestUnknownID(t *testing.T) {
	mgr, _ := newTestManager(t, 2)
	mustVerdict(t, mgr, Response{ID: 7}, Unknown)
	mustVerdict(t, mgr, Response{ID: 0}, Unknown)
}

func TestConfigValidation(t *testing.T) {
	if _, err := NewManager(Config{Space: 0, Isolation: time.Second}); err == nil {
		t.Fatal("Space 0 accepted")
	}
	if _, err := NewManager(Config{Space: 65537, Isolation: time.Second}); err == nil {
		t.Fatal("Space 65537 accepted")
	}
	if _, err := NewManager(Config{Space: 1, Isolation: 0}); err == nil {
		t.Fatal("zero isolation accepted")
	}
	if _, err := NewManager(Config{Space: 1, Isolation: time.Second}); err != nil {
		t.Fatalf("default clock/rand config: %v", err)
	}
}
