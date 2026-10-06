package pdp_test

import (
	"testing"

	"pdp"
)

// lineInstance returns a line-metric instance: nodes 0..6 on a line,
// dist(i,j) = 2*|i-j|, depot at 0, capacity 5.
func lineInstance() pdp.Instance {
	dist := make([][]int, 7)
	for i := range dist {
		dist[i] = make([]int, 7)
		for j := range dist[i] {
			d := i - j
			if d < 0 {
				d = -d
			}
			dist[i][j] = 2 * d
		}
	}
	return pdp.Instance{Depot: 0, Capacity: 5, Dist: dist}
}

func lineRequests() []pdp.Request {
	return []pdp.Request{
		{ID: 1, Pickup: 1, Delivery: 4, Weight: 2},
		{ID: 2, Pickup: 2, Delivery: 5, Weight: 2},
		{ID: 3, Pickup: 3, Delivery: 6, Weight: 1},
	}
}

func newSim(t *testing.T) (*pdp.Simulator, pdp.Instance, []pdp.Request) {
	t.Helper()
	in := lineInstance()
	reqs := lineRequests()
	sim, err := pdp.NewSimulator(in, reqs)
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	return sim, in, reqs
}

func mustStatus(t *testing.T, sim *pdp.Simulator, reqID int) pdp.ReqStatus {
	t.Helper()
	st, err := sim.Status(reqID)
	if err != nil {
		t.Fatalf("Status(%d): %v", reqID, err)
	}
	return st
}

// expectedTasks reconstructs the remaining work from the simulator's
// public state so the brute-force enumerator can verify the replanned
// route. returns maps request id to its cancellation return point.
func expectedTasks(t *testing.T, sim *pdp.Simulator, reqs []pdp.Request, returns map[int]int) []bfTask {
	t.Helper()
	var tasks []bfTask
	for _, r := range reqs {
		switch mustStatus(t, sim, r.ID) {
		case pdp.StatusPending:
			tasks = append(tasks, bfTask{reqID: r.ID, pickup: r.Pickup, drop: r.Delivery, weight: r.Weight})
		case pdp.StatusOnboard:
			tasks = append(tasks, bfTask{reqID: r.ID, pickup: -1, drop: r.Delivery, weight: r.Weight})
		case pdp.StatusReturning:
			tasks = append(tasks, bfTask{reqID: r.ID, pickup: -1, drop: returns[r.ID], weight: r.Weight})
		}
	}
	return tasks
}

// checkPlan verifies the simulator's remaining plan against the independent
// brute-force enumeration and validates the load trajectory evidence.
func checkPlan(t *testing.T, sim *pdp.Simulator, in pdp.Instance, reqs []pdp.Request, returns map[int]int) {
	t.Helper()
	plan := sim.Plan()
	want := bruteForce(in, expectedTasks(t, sim, reqs, returns), sim.Pos(), sim.Load())
	if plan.Cost != want.Cost {
		t.Fatalf("remaining cost %d, brute force says %d", plan.Cost, want.Cost)
	}
	if !equalInts(plan.Nodes, want.Nodes) {
		t.Fatalf("remaining nodes %v, brute force says %v", plan.Nodes, want.Nodes)
	}
	if len(plan.Loads) != len(plan.Stops)+1 || plan.Loads[0] != sim.Load() {
		t.Fatalf("load trajectory %v inconsistent with %d stops and current load %d",
			plan.Loads, len(plan.Stops), sim.Load())
	}
	for i, st := range plan.Stops {
		if plan.Loads[i+1] != st.LoadAfter {
			t.Fatalf("stop %d load evidence %d != trajectory %d", i, st.LoadAfter, plan.Loads[i+1])
		}
		if st.LoadAfter < 0 || st.LoadAfter > in.Capacity {
			t.Fatalf("stop %d load %d outside [0,%d]", i, st.LoadAfter, in.Capacity)
		}
	}
}

// advanceUntil executes the plan one stop at a time until cond holds.
func advanceUntil(t *testing.T, sim *pdp.Simulator, what string, cond func() bool) {
	t.Helper()
	for !cond() {
		if len(sim.Plan().Stops) == 0 {
			t.Fatalf("route finished before condition %q held", what)
		}
		if err := sim.Advance(1); err != nil {
			t.Fatalf("Advance: %v", err)
		}
	}
}

// Cancellation before pickup voids the request: it disappears from the
// remaining plan, which stays optimal per brute force.
func TestCancelBeforePickup(t *testing.T) {
	sim, in, reqs := newSim(t)
	if err := sim.Advance(1); err != nil { // one pickup done, prefix fixed
		t.Fatal(err)
	}
	target := -1
	for _, r := range reqs {
		if mustStatus(t, sim, r.ID) == pdp.StatusPending {
			target = r.ID
			break
		}
	}
	if target < 0 {
		t.Fatal("no pending request to cancel")
	}
	if err := sim.Cancel(target, 0); err != nil {
		t.Fatal(err)
	}
	if st := mustStatus(t, sim, target); st != pdp.StatusVoided {
		t.Fatalf("status %v, want voided", st)
	}
	for _, st := range sim.Plan().Stops {
		if st.ReqID == target {
			t.Fatalf("voided request %d still planned at %v", target, st)
		}
	}
	checkPlan(t, sim, in, reqs, nil)
}

// Cancellation right at the pickup moment finds the request onboard: it
// must be rerouted to the return point and keep occupying capacity.
func TestCancelAtPickupMoment(t *testing.T) {
	sim, in, reqs := newSim(t)
	returns := map[int]int{}
	target := reqs[0].ID
	advanceUntil(t, sim, "target onboard", func() bool {
		return mustStatus(t, sim, target) == pdp.StatusOnboard
	})
	// The pickup is the last executed stop; cancel now, before moving on.
	returns[target] = 6
	if err := sim.Cancel(target, returns[target]); err != nil {
		t.Fatal(err)
	}
	if st := mustStatus(t, sim, target); st != pdp.StatusReturning {
		t.Fatalf("status %v, want returning", st)
	}
	assertReturnStop(t, sim, in, reqs, target, returns[target])
	checkPlan(t, sim, in, reqs, returns)
}

// Cancellation en route (pickup done, more stops executed, delivery not
// yet done) behaves like the pickup-moment case.
func TestCancelEnRoute(t *testing.T) {
	sim, in, reqs := newSim(t)
	returns := map[int]int{}
	target := reqs[0].ID
	advanceUntil(t, sim, "target onboard", func() bool {
		return mustStatus(t, sim, target) == pdp.StatusOnboard
	})
	// Drive on: execute more stops while the target stays onboard.
	advanceUntil(t, sim, "two more stops or target delivered", func() bool {
		st := mustStatus(t, sim, target)
		return len(sim.History()) >= 3 || st != pdp.StatusOnboard
	})
	if st := mustStatus(t, sim, target); st != pdp.StatusOnboard {
		t.Fatalf("target left the vehicle before cancellation: %v", st)
	}
	returns[target] = 1
	if err := sim.Cancel(target, returns[target]); err != nil {
		t.Fatal(err)
	}
	if st := mustStatus(t, sim, target); st != pdp.StatusReturning {
		t.Fatalf("status %v, want returning", st)
	}
	assertReturnStop(t, sim, in, reqs, target, returns[target])
	checkPlan(t, sim, in, reqs, returns)
}

// A cancellation arriving at the same moment as the delivery finds the
// delivery already inside the fixed prefix: it has no effect.
func TestCancelAtDeliveryMoment(t *testing.T) {
	sim, in, reqs := newSim(t)
	target := reqs[0].ID
	advanceUntil(t, sim, "target delivered", func() bool {
		return mustStatus(t, sim, target) == pdp.StatusDelivered
	})
	before := sim.Plan()
	if err := sim.Cancel(target, 6); err != nil {
		t.Fatal(err)
	}
	if st := mustStatus(t, sim, target); st != pdp.StatusDelivered {
		t.Fatalf("status %v, want delivered (cancel must be a no-op)", st)
	}
	after := sim.Plan()
	if before.Cost != after.Cost || !equalInts(before.Nodes, after.Nodes) {
		t.Fatalf("plan changed after no-op cancel: %v -> %v", before.Nodes, after.Nodes)
	}
	checkPlan(t, sim, in, reqs, nil)
}

// assertReturnStop checks that the remaining plan contains exactly one
// return stop for the cancelled request at the return point, and that the
// load evidence proves the request keeps occupying capacity until then.
func assertReturnStop(t *testing.T, sim *pdp.Simulator, in pdp.Instance, reqs []pdp.Request, target, returnPoint int) {
	t.Helper()
	var weight int
	for _, r := range reqs {
		if r.ID == target {
			weight = r.Weight
		}
	}
	plan := sim.Plan()
	found := 0
	load := sim.Load()
	for i, st := range plan.Stops {
		if st.ReqID == target {
			found++
			if st.Kind != pdp.ReturnEvent || st.Node != returnPoint {
				t.Fatalf("cancelled request served as %v at %d, want return at %d", st.Kind, st.Node, returnPoint)
			}
			if load < weight {
				t.Fatalf("load %d before return drop excludes cancelled weight %d", load, weight)
			}
			load -= weight
			if st.LoadAfter != load {
				t.Fatalf("return drop load evidence %d, recomputed %d", st.LoadAfter, load)
			}
			continue
		}
		for _, r := range reqs {
			if r.ID == st.ReqID {
				if st.Kind == pdp.PickupEvent {
					load += r.Weight
				} else {
					load -= r.Weight
				}
			}
		}
		_ = i
	}
	if found != 1 {
		t.Fatalf("found %d return stops for cancelled request, want 1", found)
	}
}

// A scripted sequence of events: after every event the remaining plan must
// be optimal (checked against full-permutation brute force), the executed
// prefix must stay fixed, and the journal must record per-request state
// and load evidence for every action.
func TestCancellationSequence(t *testing.T) {
	sim, in, reqs := newSim(t)
	returns := map[int]int{}

	total := sim.CostSoFar() + sim.Plan().Cost
	prefixLen := 0
	journalActions := 1 // initial "plan" entry

	step := func(action func(), wantActions int) {
		t.Helper()
		action()
		journalActions += wantActions
		// Fixed prefix: history only grows, never changes.
		if len(sim.History()) < prefixLen {
			t.Fatal("executed prefix shrank")
		}
		prefixLen = len(sim.History())
		checkPlan(t, sim, in, reqs, returns)
		journal := sim.Journal()
		if len(journal) != journalActions {
			t.Fatalf("journal has %d entries, want %d", len(journal), journalActions)
		}
		last := journal[len(journal)-1]
		if last.Pos != sim.Pos() || last.Load != sim.Load() || last.CostSoFar != sim.CostSoFar() {
			t.Fatalf("journal evidence %+v mismatches simulator state", last)
		}
		for _, r := range reqs {
			if last.Status[r.ID] != mustStatus(t, sim, r.ID) {
				t.Fatalf("journal status of %d = %v, simulator says %v",
					r.ID, last.Status[r.ID], mustStatus(t, sim, r.ID))
			}
		}
	}

	// Event 1: drive one stop, then cancel a pending request.
	step(func() {
		if err := sim.Advance(1); err != nil {
			t.Fatal(err)
		}
	}, 1)
	step(func() {
		pending := -1
		for _, r := range reqs {
			if mustStatus(t, sim, r.ID) == pdp.StatusPending {
				pending = r.ID
			}
		}
		if pending < 0 {
			t.Fatal("no pending request")
		}
		if err := sim.Cancel(pending, 0); err != nil {
			t.Fatal(err)
		}
	}, 1)

	// Event 2: drive two more stops, then cancel an onboard request.
	step(func() {
		if err := sim.Advance(2); err != nil {
			t.Fatal(err)
		}
	}, 1)
	step(func() {
		onboard := -1
		for _, r := range reqs {
			if mustStatus(t, sim, r.ID) == pdp.StatusOnboard {
				onboard = r.ID
				break
			}
		}
		if onboard < 0 {
			t.Fatal("no onboard request")
		}
		returns[onboard] = 6
		if err := sim.Cancel(onboard, returns[onboard]); err != nil {
			t.Fatal(err)
		}
	}, 1)

	// Event 3: finish the route; the total cost of prefix + remainder is
	// conserved between events that do not change the task set.
	beforeTotal := sim.CostSoFar() + sim.Plan().Cost
	step(func() {
		if err := sim.Advance(len(sim.Plan().Stops)); err != nil {
			t.Fatal(err)
		}
	}, 1)
	if got := sim.CostSoFar() + sim.Plan().Cost; got != beforeTotal {
		t.Fatalf("prefix+remainder cost %d changed to %d without new events", beforeTotal, got)
	}
	_ = total

	// Every request must be in a terminal state now.
	for _, r := range reqs {
		switch mustStatus(t, sim, r.ID) {
		case pdp.StatusDelivered, pdp.StatusReturned, pdp.StatusVoided:
		default:
			t.Fatalf("request %d in non-terminal state %v after full route", r.ID, mustStatus(t, sim, r.ID))
		}
	}
	if len(sim.Plan().Stops) != 0 {
		t.Fatalf("plan not empty after full route: %v", sim.Plan().Stops)
	}
	if sim.Load() != 0 {
		t.Fatalf("vehicle not empty at end: load %d", sim.Load())
	}
}
