package main

import "fmt"

// originalTrace is the long failing schedule. Every message is accounted for
// (quiescent schedule). It contains noise timeouts that fire harmlessly
// (on idle or already-decided nodes) plus the two events that actually cause
// the split decision: the lost Commit to P2 and P2's timeout.
func originalTrace() []Action {
	return []Action{
		{Kind: ActRequest},
		{Kind: ActTimeout, Node: NodeP1}, // noise: P1 idle, no-op
		{Kind: ActDeliver, Key: "Prepare:C->P1#1"},
		{Kind: ActDeliver, Key: "Prepare:C->P2#1"},
		{Kind: ActDeliver, Key: "Yes:P1->C#1"},
		{Kind: ActDeliver, Key: "Yes:P2->C#1"},
		{Kind: ActDeliver, Key: "Commit:C->P1#1"},
		{Kind: ActTimeout, Node: NodeCoord},    // noise: coordinator already decided
		{Kind: ActTimeout, Node: NodeP1},       // noise: P1 already decided
		{Kind: ActDrop, Key: "Commit:C->P2#1"}, // NECESSARY fault (packet loss)
		{Kind: ActTimeout, Node: NodeP2},       // NECESSARY timeout
	}
}

// faultFreeTrace drives the protocol with no faults at all, delivering every
// pending message in FIFO order. Used as the liveness witness.
func faultFreeTrace() []Action {
	actions := []Action{{Kind: ActRequest}}
	s := NewSim()
	s.Request()
	for len(s.pending) > 0 {
		m := s.pending[0]
		actions = append(actions, Action{Kind: ActDeliver, Key: m.Key})
		s.take(m.Key)
		s.deliver(m)
	}
	return actions
}

func printTrace(title string, trace []Action) {
	fmt.Printf("%s (%d events):\n", title, len(trace))
	for i, a := range trace {
		fmt.Printf("  %2d. %s\n", i+1, a)
	}
}

func mustReplay(trace []Action) Result {
	res, err := Replay(trace)
	if err != nil {
		panic(err)
	}
	return res
}

func main() {
	orig := originalTrace()

	fmt.Println("=== 1. Original failing trace ===")
	printTrace("original", orig)
	res := mustReplay(orig)
	sig := Violation(res.Decisions)
	fmt.Printf("replay: %s\n", describe(res))
	fmt.Printf("SAFETY VIOLATION: %s\n\n", sig)

	fmt.Println("=== 2. Shrinking (remove irrelevant faults/deliveries, keep the failure) ===")
	minimal, steps, err := Shrink(orig)
	if err != nil {
		panic(err)
	}
	for _, st := range steps {
		mark := "keep-evt"
		if st.Accepted {
			mark = "REMOVED "
		}
		fmt.Printf("  [%s] remove %-26s -> %s\n", mark, st.Removed, st.Reason)
	}
	fmt.Println()

	fmt.Println("=== 3. Minimal replayable counterexample (safety evidence) ===")
	printTrace("minimal", minimal)
	mres := mustReplay(minimal)
	fmt.Printf("replay: %s\n", describe(mres))
	fmt.Printf("SAFETY VIOLATION (evidence): %s\n", Violation(mres.Decisions))
	fmt.Println("replay log:")
	for _, l := range mres.Log {
		fmt.Println("   ", l)
	}
	fmt.Println()

	fmt.Println("=== 4. Liveness evidence (fault-free run terminates, all commit) ===")
	ff := faultFreeTrace()
	printTrace("fault-free", ff)
	fres := mustReplay(ff)
	fmt.Printf("replay: %s\n", describe(fres))
	fmt.Printf("violation in fault-free run: %q (none expected)\n\n", Violation(fres.Decisions))

	fmt.Println("=== 5. Necessary loss / timeout are load-bearing ===")
	// 5a. Remove the necessary timeout: P2 never decides. A stuck node is a
	// liveness symptom, NOT the safety error.
	stuck := mustReplay(minimal[:len(minimal)-1])
	fmt.Printf("minimal without Timeout(P2): %s (P2 undecided)\n", describe(stuck))
	fmt.Printf("  classified as safety violation: %v\n", Violation(stuck.Decisions) != "")
	// 5b. Replace the necessary loss by a delivery: everyone commits.
	healed := append([]Action(nil), minimal...)
	for i, a := range healed {
		if a.Kind == ActDrop {
			healed[i] = Action{Kind: ActDeliver, Key: a.Key}
		}
	}
	hres := mustReplay(healed)
	fmt.Printf("minimal with Drop->Deliver(Commit:C->P2#1): %s\n", describe(hres))
	fmt.Printf("  classified as safety violation: %v\n", Violation(hres.Decisions) != "")
}
