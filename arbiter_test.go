package leaseaudit

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeClock is a manually advanced time source for deterministic tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}
}

func (f *fakeClock) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeClock) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

// node simulates a lease client with its own Lamport clock.
type node struct {
	id    string
	clock *LamportClock
}

func newNode(id string) *node { return &node{id: id, clock: &LamportClock{}} }

func (n *node) acquire(a *Arbiter, leaseID string, ttl time.Duration) Response {
	return a.Acquire(n.id, leaseID, n.clock.Tick(), ttl)
}

func (n *node) renew(a *Arbiter, leaseID string, term uint64, ttl time.Duration) Response {
	return a.Renew(n.id, leaseID, n.clock.Tick(), term, ttl)
}

func eventsOfKind(events []Event, kind EventKind) []Event {
	var out []Event
	for _, e := range events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func mustFind(t *testing.T, events []Event, kind EventKind, nodeID string) Event {
	t.Helper()
	for _, e := range events {
		if e.Kind == kind && e.NodeID == nodeID {
			return e
		}
	}
	t.Fatalf("no event kind=%s node=%s", kind, nodeID)
	return Event{}
}

// TestRaceContentionAuditTrail deterministically reproduces the race
// where two nodes request the master lease within the same
// millisecond: node A's request is held inside the arbiter's decision
// critical section while node B's request arrives. The audit trail
// must capture arrival order, lock state transitions, the conflict,
// and the rejection with a complete causal chain.
func TestRaceContentionAuditTrail(t *testing.T) {
	log := NewAuditLog()
	clock := newFakeClock()

	aEntered := make(chan struct{})
	releaseA := make(chan struct{})
	arrived := make(chan Event, 8)

	var hookOnce sync.Once
	arb := NewArbiter(log,
		WithClock(clock.now),
		WithDecideHook(func() {
			// Only stall the first request that reaches the decision
			// point, forcing B to arrive mid-arbitration.
			hookOnce.Do(func() {
				close(aEntered)
				<-releaseA
			})
		}),
		WithArrivalHook(func(ev Event) { arrived <- ev }),
	)

	nodeA, nodeB := newNode("node-A"), newNode("node-B")

	var respA, respB Response
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); respA = nodeA.acquire(arb, "master", 30*time.Second) }()

	<-aEntered // A is inside the decision critical section

	go func() { defer wg.Done(); respB = nodeB.acquire(arb, "master", 30*time.Second) }()

	// Wait until B's arrival (and the race conflict) is recorded.
	timeout := time.After(2 * time.Second)
	for log.Len() < 3 {
		select {
		case <-arrived:
		case <-timeout:
			t.Fatal("node B never arrived")
		}
	}
	close(releaseA)
	wg.Wait()

	// --- verdicts ---
	if !respA.Granted {
		t.Fatalf("node A should win the lease, got %+v", respA)
	}
	if respB.Granted {
		t.Fatalf("node B must be rejected, got %+v", respB)
	}
	if respA.Term != 1 || respA.Holder != "node-A" {
		t.Fatalf("unexpected lease state: %+v", respA)
	}

	events := log.Events()

	// --- arrival order evidence ---
	arrivalA := mustFind(t, events, EventRequestArrived, "node-A")
	arrivalB := mustFind(t, events, EventRequestArrived, "node-B")
	if arrivalA.Seq >= arrivalB.Seq {
		t.Fatalf("arrival order wrong: A=#%d B=#%d", arrivalA.Seq, arrivalB.Seq)
	}
	if arrivalA.Lamport >= arrivalB.Lamport {
		t.Fatalf("lamport order wrong: A=%d B=%d", arrivalA.Lamport, arrivalB.Lamport)
	}
	// Both requests arrived within the same millisecond (wall clock).
	if d := arrivalB.WallNano - arrivalA.WallNano; d < 0 || d > int64(time.Millisecond) {
		t.Fatalf("arrivals not within 1ms: %dns", d)
	}

	// --- race conflict evidence ---
	conflicts := eventsOfKind(events, EventRaceConflict)
	if len(conflicts) != 1 {
		t.Fatalf("want exactly 1 race conflict, got %d", len(conflicts))
	}
	conflict := conflicts[0]
	wantCauses := []int64{arrivalA.Seq, arrivalB.Seq}
	if fmt.Sprint(conflict.Causes) != fmt.Sprint(wantCauses) {
		t.Fatalf("conflict causes = %v, want %v", conflict.Causes, wantCauses)
	}

	// --- arbiter lock state transitions ---
	locks := eventsOfKind(events, EventLockAcquired)
	unlocks := eventsOfKind(events, EventLockReleased)
	if len(locks) != 2 || len(unlocks) != 2 {
		t.Fatalf("want 2 lock/unlock pairs, got %d/%d", len(locks), len(unlocks))
	}
	// B's critical section starts only after A's ends (serialization).
	if locks[1].Seq <= unlocks[0].Seq {
		t.Fatalf("lock transitions overlap: acquire#%d before release#%d", locks[1].Seq, unlocks[0].Seq)
	}

	// --- grant / rejection ---
	grant := mustFind(t, events, EventLeaseGranted, ArbiterNodeID)
	reject := mustFind(t, events, EventLeaseRejected, ArbiterNodeID)
	if grant.Term != 1 {
		t.Fatalf("grant term = %d, want 1", grant.Term)
	}
	// B arrived before A's grant was recorded: the race is real.
	if arrivalB.Seq >= grant.Seq {
		t.Fatalf("B arrival #%d should precede grant #%d", arrivalB.Seq, grant.Seq)
	}
	// The rejection is caused by B's arrival AND A's grant.
	wantRejectCauses := []int64{arrivalB.Seq, grant.Seq}
	if fmt.Sprint(reject.Causes) != fmt.Sprint(wantRejectCauses) {
		t.Fatalf("reject causes = %v, want %v", reject.Causes, wantRejectCauses)
	}

	// --- causal evidence chain from the rejection backwards ---
	chain := CausalChain(log, reject.Seq)
	chainSeqs := make([]int64, len(chain))
	for i, e := range chain {
		chainSeqs[i] = e.Seq
	}
	for _, want := range []int64{arrivalA.Seq, arrivalB.Seq, grant.Seq, reject.Seq} {
		found := false
		for _, s := range chainSeqs {
			if s == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("causal chain %v missing event #%d", chainSeqs, want)
		}
	}
	// Chain is chronological.
	for i := 1; i < len(chainSeqs); i++ {
		if chainSeqs[i] < chainSeqs[i-1] {
			t.Fatalf("chain not chronological: %v", chainSeqs)
		}
	}

	// --- replay reconstructs the final state ---
	timeline := Replay(events)
	final := timeline[len(timeline)-1].State
	if final.Holder != "node-A" || final.Term != 1 || final.Locked {
		t.Fatalf("replayed final state = %+v", final)
	}
}

// TestLeaseLifecycle covers renew, handover, stale-term fencing,
// expiry and re-acquisition, asserting each audited transition.
func TestLeaseLifecycle(t *testing.T) {
	log := NewAuditLog()
	clock := newFakeClock()
	arb := NewArbiter(log, WithClock(clock.now))
	a, b := newNode("node-A"), newNode("node-B")

	if r := a.acquire(arb, "master", 10*time.Second); !r.Granted || r.Term != 1 {
		t.Fatalf("acquire: %+v", r)
	}
	if r := a.renew(arb, "master", 1, 10*time.Second); !r.Granted {
		t.Fatalf("renew by holder: %+v", r)
	}
	if r := b.renew(arb, "master", 1, 10*time.Second); r.Granted {
		t.Fatalf("renew by non-holder must fail: %+v", r)
	}
	if r := a.renew(arb, "master", 99, 10*time.Second); r.Granted {
		t.Fatalf("renew with stale term must fail: %+v", r)
	}
	if r := arb.Handover("node-A", "node-B", "master", a.clock.Tick(), 1, 10*time.Second); !r.Granted || r.Term != 2 {
		t.Fatalf("handover: %+v", r)
	}
	if r := a.renew(arb, "master", 1, 10*time.Second); r.Granted {
		t.Fatalf("old holder must be fenced off after handover: %+v", r)
	}

	clock.advance(11 * time.Second)
	if !arb.Sweep("master") {
		t.Fatal("sweep should expire the lease")
	}
	if r := a.acquire(arb, "master", 10*time.Second); !r.Granted || r.Term != 3 {
		t.Fatalf("re-acquire after expiry: %+v", r)
	}

	events := log.Events()
	for _, kind := range []EventKind{EventLeaseGranted, EventLeaseRenewed, EventLeaseHandover, EventLeaseRejected, EventLeaseExpired} {
		if len(eventsOfKind(events, kind)) == 0 {
			t.Fatalf("missing audited event kind %s", kind)
		}
	}
	final := Replay(events)
	if last := final[len(final)-1].State; last.Holder != "node-A" || last.Term != 3 {
		t.Fatalf("replayed final state = %+v", last)
	}
}

// TestConcurrentStress hammers the arbiter with racing goroutines and
// verifies (under -race) that exactly one wins and the log stays
// internally consistent.
func TestConcurrentStress(t *testing.T) {
	log := NewAuditLog()
	arb := NewArbiter(log) // real clock, real contention

	const racers = 32
	responses := make([]Response, racers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			n := newNode(fmt.Sprintf("node-%02d", i))
			<-start
			responses[i] = n.acquire(arb, "master", time.Minute)
		}(i)
	}
	close(start)
	wg.Wait()

	grants := 0
	for _, r := range responses {
		if r.Granted {
			grants++
		}
	}
	if grants != 1 {
		t.Fatalf("exactly one racer may win, got %d", grants)
	}

	events := log.Events()
	if got := len(eventsOfKind(events, EventRequestArrived)); got != racers {
		t.Fatalf("arrivals = %d, want %d", got, racers)
	}
	if got := len(eventsOfKind(events, EventLeaseGranted)); got != 1 {
		t.Fatalf("grants = %d, want 1", got)
	}
	if got := len(eventsOfKind(events, EventLeaseRejected)); got != racers-1 {
		t.Fatalf("rejections = %d, want %d", got, racers-1)
	}
	// Every rejection must cite the grant that fenced it off.
	grant := eventsOfKind(events, EventLeaseGranted)[0]
	for _, rej := range eventsOfKind(events, EventLeaseRejected) {
		if len(rej.Causes) != 2 || rej.Causes[1] != grant.Seq {
			t.Fatalf("rejection #%d causes = %v, want [arrival, grant#%d]", rej.Seq, rej.Causes, grant.Seq)
		}
	}
	// Replay must end with a single holder and a free lock.
	final := Replay(events)[len(events)-1].State
	if final.Holder == "" || final.Locked {
		t.Fatalf("final replayed state = %+v", final)
	}
}
