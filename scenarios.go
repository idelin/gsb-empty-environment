package main

// goodScenario: two clients against a 3-replica ABD quorum register,
// fault-free. Expect: all ops complete (liveness) and a linearizable
// history (safety).
func goodScenario() (SimReport, *Sim) {
	sim := NewSim(Faults{})
	reps := []*Replica{{name: "r0"}, {name: "r1"}, {name: "r2"}}
	nextID := 1
	c1 := &Client{id: 1, sim: sim, reps: reps, nextID: &nextID, script: []Op{
		{Kind: OpWrite, Val: 1},
		{Kind: OpWrite, Val: 3},
	}}
	c2 := &Client{id: 2, sim: sim, reps: reps, nextID: &nextID, script: []Op{
		{Kind: OpRead},
		{Kind: OpWrite, Val: 2},
		{Kind: OpRead},
	}}
	c1.start(0)
	c2.start(0)
	return runClients(sim, []*Client{c1, c2}, 10000), sim
}

// crashScenario: same register, but replica r2 crashes at t=0. A quorum
// of 2/3 is still available. Expect: liveness preserved, history still
// linearizable.
func crashScenario() (SimReport, *Sim) {
	sim := NewSim(Faults{})
	reps := []*Replica{{name: "r0"}, {name: "r1"}, {name: "r2"}}
	reps[2].crashed = true
	nextID := 1
	c1 := &Client{id: 1, sim: sim, reps: reps, nextID: &nextID, script: []Op{
		{Kind: OpWrite, Val: 7},
		{Kind: OpRead},
	}}
	c2 := &Client{id: 2, sim: sim, reps: reps, nextID: &nextID, script: []Op{
		{Kind: OpRead},
		{Kind: OpWrite, Val: 8},
	}}
	c1.start(0)
	c2.start(0)
	return runClients(sim, []*Client{c1, c2}, 10000), sim
}

// badScenario: client 2 uses the buggy single-replica read, and the
// write's STORE message to replica r0 is held until t=50. The write
// completes on the {r1,r2} quorum; the later buggy read hits stale r0
// and returns the initial value, violating real-time order.
// Expect: NOT LINEARIZABLE with counterexample evidence.
func badScenario() (SimReport, *Sim) {
	sim := NewSim(Faults{Hold: map[string]int64{"c1o0>r0": 50}})
	reps := []*Replica{{name: "r0"}, {name: "r1"}, {name: "r2"}}
	nextID := 1
	c1 := &Client{id: 1, sim: sim, reps: reps, nextID: &nextID, script: []Op{
		{Kind: OpWrite, Val: 1},
	}}
	c2 := &Client{id: 2, sim: sim, reps: reps, buggy: true, nextID: &nextID, script: []Op{
		{Kind: OpRead},
	}}
	c1.start(0)
	c2.start(10) // invoked strictly after c1's write has responded
	return runClients(sim, []*Client{c1, c2}, 10000), sim
}

// pendingHistory: hand-built history exercising the pending-call
// extension semantics. The pending write #2 is the only justification
// for read #3 returning 1; the pending read #4 is a wildcard.
func pendingHistory() []Op {
	return []Op{
		{ID: 1, Kind: OpWrite, Val: 0, Invoke: 0, Respond: 2},
		{ID: 2, Kind: OpWrite, Val: 1, Invoke: 3, Pending: true}, // may be kept or dropped
		{ID: 3, Kind: OpRead, Val: 1, Invoke: 4, Respond: 6},     // needs pending write #2
		{ID: 4, Kind: OpRead, Invoke: 7, Pending: true},          // wildcard
	}
}

// hardHistory: 9 mutually overlapping writes plus a read that forces
// write #1 to be linearized last. The search space is large enough that
// a small budget is exhausted. Expect: UNKNOWN under a tight budget,
// LINEARIZABLE with a full budget.
func hardHistory() []Op {
	ops := []Op{}
	for i := 1; i <= 9; i++ {
		ops = append(ops, Op{ID: i, Kind: OpWrite, Val: i, Invoke: int64(i), Respond: 100})
	}
	ops = append(ops, Op{ID: 10, Kind: OpRead, Val: 1, Invoke: 101, Respond: 102})
	return ops
}
