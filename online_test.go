package pdp

import (
	"math/rand"
	"strings"
	"testing"
)

func itemStatus(t *testing.T, snap Snapshot, request int) ItemStatus {
	t.Helper()
	for _, it := range snap.Items {
		if it.Request == request {
			return it.Status
		}
	}
	t.Fatalf("request %d missing from snapshot", request)
	return 0
}

// twoRequestProblem: depot 0, R0 1->2 (w3), R1 3->4 (w3), capacity 5 so
// both never fit on board together.
func twoRequestProblem() Problem {
	return Problem{
		NumNodes: 5,
		Dist:     completeDist(5, 1),
		Capacity: 5,
		Start:    0,
		Requests: []Request{
			{ID: 0, Pickup: 1, Drop: 2, Weight: 3},
			{ID: 1, Pickup: 3, Drop: 4, Weight: 3},
		},
	}
}

func TestCancelBeforePickupRemovesRequest(t *testing.T) {
	sim, err := NewSimulator(twoRequestProblem())
	if err != nil {
		t.Fatal(err)
	}
	snap, err := sim.Apply(Cancel(1, 4))
	if err != nil {
		t.Fatal(err)
	}
	if got := itemStatus(t, snap, 1); got != StatusCancelled {
		t.Fatalf("status %v, want cancelled", got)
	}
	// Only R0 remains: pick at 1, deliver at 2.
	if want := []int{1, 2}; !equalSeq(snap.Remaining.Nodes(), want) {
		t.Fatalf("remaining nodes %v, want %v", snap.Remaining.Nodes(), want)
	}
	for _, st := range snap.Remaining.Stops {
		if st.Request == 1 {
			t.Fatalf("cancelled request still planned: %+v", st)
		}
	}
	if err := sim.CrossCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestCancelEnRouteKeepsOccupyingCapacity(t *testing.T) {
	sim, err := NewSimulator(twoRequestProblem())
	if err != nil {
		t.Fatal(err)
	}
	// Initial plan must be 1,2,3,4 (cannot co-load 3+3 > 5).
	if want := []int{1, 2, 3, 4}; !equalSeq(sim.Plan().Nodes(), want) {
		t.Fatalf("initial plan %v, want %v", sim.Plan().Nodes(), want)
	}
	if _, err := sim.Apply(Step()); err != nil { // pick R0 at node 1
		t.Fatal(err)
	}
	snap, err := sim.Apply(Cancel(0, 2)) // return point: node 2
	if err != nil {
		t.Fatal(err)
	}
	if got := itemStatus(t, snap, 0); got != StatusReturning {
		t.Fatalf("status %v, want returning", got)
	}
	// Load evidence: R0 still on board right after the cancel.
	if snap.Load != 3 {
		t.Fatalf("load %d, want 3 (cancelled item still on board)", snap.Load)
	}
	// R0 (w3) occupies capacity until returned, so R1 (w3) can only be
	// picked after the return stop at node 2.
	if want := []int{2, 3, 4}; !equalSeq(snap.Remaining.Nodes(), want) {
		t.Fatalf("remaining nodes %v, want %v", snap.Remaining.Nodes(), want)
	}
	st := snap.Remaining.Stops[0]
	if st.Kind != ReturnStop || st.Request != 0 || st.Node != 2 {
		t.Fatalf("first remaining stop %+v, want return of R0 at 2", st)
	}
	if want := []int{0, 3, 0}; !equalSeq(snap.Remaining.Loads(), want) {
		t.Fatalf("remaining loads %v, want %v", snap.Remaining.Loads(), want)
	}
	if err := sim.CrossCheck(); err != nil {
		t.Fatal(err)
	}
	// Drive the return home; R0 becomes returned and frees capacity.
	snap, err = sim.Apply(Step())
	if err != nil {
		t.Fatal(err)
	}
	if got := itemStatus(t, snap, 0); got != StatusReturned {
		t.Fatalf("status %v, want returned", got)
	}
	if snap.Load != 0 {
		t.Fatalf("load %d after return, want 0", snap.Load)
	}
	if err := sim.CrossCheck(); err != nil {
		t.Fatal(err)
	}
}

// Cancellation at the very moment of delivery is ordered by event
// submission: cancel-then-deliver-step finds the item on board and forces
// a return; deliver-step-then-cancel finds it delivered and is rejected.
func TestCancelAtDeliveryMomentOrdering(t *testing.T) {
	prob := Problem{
		NumNodes: 4,
		Dist:     completeDist(4, 1),
		Capacity: 2,
		Start:    0,
		Requests: []Request{{ID: 0, Pickup: 1, Drop: 2, Weight: 1}},
	}

	// Order 1: cancel arrives just before the delivery step executes.
	sim, err := NewSimulator(prob)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sim.Apply(Step()); err != nil { // pick at 1
		t.Fatal(err)
	}
	snap, err := sim.Apply(Cancel(0, 3)) // same moment as planned delivery
	if err != nil {
		t.Fatal(err)
	}
	if got := itemStatus(t, snap, 0); got != StatusReturning {
		t.Fatalf("cancel-first: status %v, want returning", got)
	}
	if want := []int{3}; !equalSeq(snap.Remaining.Nodes(), want) {
		t.Fatalf("cancel-first: remaining %v, want %v", snap.Remaining.Nodes(), want)
	}
	if snap.Remaining.Stops[0].Kind != ReturnStop {
		t.Fatalf("cancel-first: kind %v, want return", snap.Remaining.Stops[0].Kind)
	}
	if err := sim.CrossCheck(); err != nil {
		t.Fatal(err)
	}

	// Order 2: the delivery step executes just before the cancel arrives.
	sim2, err := NewSimulator(prob)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sim2.Apply(Step()); err != nil { // pick at 1
		t.Fatal(err)
	}
	snap, err = sim2.Apply(Step()) // deliver at 2
	if err != nil {
		t.Fatal(err)
	}
	if got := itemStatus(t, snap, 0); got != StatusDelivered {
		t.Fatalf("deliver-first: status %v, want delivered", got)
	}
	if _, err := sim2.Apply(Cancel(0, 3)); err == nil ||
		!strings.Contains(err.Error(), "cannot be cancelled") {
		t.Fatalf("deliver-first: cancel err %v, want rejection", err)
	}
	if err := sim2.CrossCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestCompletedPrefixIsFixed(t *testing.T) {
	sim, err := NewSimulator(twoRequestProblem())
	if err != nil {
		t.Fatal(err)
	}
	initial := sim.Plan()
	if _, err := sim.Apply(Step()); err != nil {
		t.Fatal(err)
	}
	snap, err := sim.Apply(Step())
	if err != nil {
		t.Fatal(err)
	}
	// The executed prefix equals the head of the initial plan.
	if len(snap.Executed) != 2 {
		t.Fatalf("executed %d stops, want 2", len(snap.Executed))
	}
	for i, st := range snap.Executed {
		if st != initial.Stops[i] {
			t.Fatalf("executed[%d] %+v differs from initial plan %+v", i, st, initial.Stops[i])
		}
	}
	// A cancellation must not rewrite the completed prefix.
	before := append([]Stop(nil), snap.Executed...)
	snap, err = sim.Apply(Cancel(1, 4))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Executed) != len(before) {
		t.Fatalf("prefix length changed to %d", len(snap.Executed))
	}
	for i := range before {
		if snap.Executed[i] != before[i] {
			t.Fatalf("prefix stop %d rewritten: %+v -> %+v", i, before[i], snap.Executed[i])
		}
	}
	// Load evidence of the prefix is preserved.
	if snap.Executed[0].Load != 3 || snap.Executed[1].Load != 0 {
		t.Fatalf("prefix load evidence lost: %+v", snap.Executed)
	}
	if err := sim.CrossCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestCancelTerminalAndUnknownRequests(t *testing.T) {
	sim, err := NewSimulator(twoRequestProblem())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sim.Apply(Cancel(99, 0)); err == nil {
		t.Fatal("cancel of unknown request accepted")
	}
	if _, err := sim.Apply(Cancel(0, 99)); err == nil {
		t.Fatal("cancel with out-of-range return node accepted")
	}
	if _, err := sim.Apply(Cancel(1, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := sim.Apply(Cancel(1, 0)); err == nil {
		t.Fatal("double cancel accepted")
	}
}

func TestStepOnCompletedRoute(t *testing.T) {
	prob := Problem{
		NumNodes: 2,
		Dist:     completeDist(2, 1),
		Capacity: 1,
		Start:    0,
		Requests: []Request{{ID: 0, Pickup: 1, Drop: 0, Weight: 1}},
	}
	sim, err := NewSimulator(prob)
	if err != nil {
		t.Fatal(err)
	}
	for range sim.Plan().Stops {
		if _, err := sim.Apply(Step()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sim.Apply(Step()); err == nil {
		t.Fatal("step on completed route accepted")
	}
}

// TestOnlineRandomCrossCheck drives random event streams and verifies the
// remaining plan against full permutation enumeration after every event.
func TestOnlineRandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for trial := 0; trial < 100; trial++ {
		p := randomProblem(rng, 1+rng.Intn(3))
		sim, err := NewSimulator(p)
		if err == ErrInfeasible {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := sim.CrossCheck(); err != nil {
			t.Fatalf("trial %d initial: %v", trial, err)
		}
		executed := 0
		for len(sim.Plan().Stops) > 0 {
			// Randomly cancel a live request, else advance one stop.
			var live []int
			for _, it := range sim.Current().Items {
				if it.Status == StatusPending || it.Status == StatusOnBoard {
					live = append(live, it.Request)
				}
			}
			if len(live) > 0 && rng.Intn(3) == 0 {
				if _, err := sim.Apply(Cancel(live[rng.Intn(len(live))], rng.Intn(p.NumNodes))); err != nil {
					t.Fatalf("trial %d cancel: %v", trial, err)
				}
			} else {
				if _, err := sim.Apply(Step()); err != nil {
					t.Fatalf("trial %d step: %v", trial, err)
				}
				executed++
			}
			if err := sim.CrossCheck(); err != nil {
				t.Fatalf("trial %d: %v", trial, err)
			}
			cur := sim.Current()
			if len(cur.Executed) != executed {
				t.Fatalf("trial %d: prefix length %d, want %d", trial, len(cur.Executed), executed)
			}
			if len(cur.Executed) > 0 && cur.Load != cur.Executed[len(cur.Executed)-1].Load {
				t.Fatalf("trial %d: load %d contradicts prefix evidence %d",
					trial, cur.Load, cur.Executed[len(cur.Executed)-1].Load)
			}
		}
	}
}
