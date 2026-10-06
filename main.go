package main

import (
	"fmt"

	"deadlock/checker"
)

// deadlockedSystem: A does an internal step, sends "data", then waits for
// "ack". B waits for "req" before ever sending "ack". After A's send, the
// A->B queue holds "data" which B cannot consume: deadlock.
func deadlockedSystem() *checker.System {
	a := &checker.Machine{
		Name:    "A",
		Initial: "a0",
		Transitions: []checker.Transition{
			{From: "a0", To: "a1", Act: checker.Action{Kind: checker.Tau}},
			{From: "a1", To: "a2", Act: checker.Action{Kind: checker.Send, Message: "data"}},
			{From: "a2", To: "a3", Act: checker.Action{Kind: checker.Recv, Message: "ack"}},
		},
	}
	b := &checker.Machine{
		Name:    "B",
		Initial: "b0",
		Transitions: []checker.Transition{
			{From: "b0", To: "b1", Act: checker.Action{Kind: checker.Recv, Message: "req"}},
			{From: "b1", To: "b2", Act: checker.Action{Kind: checker.Send, Message: "ack"}},
		},
	}
	return &checker.System{A: a, B: b}
}

// pingPongSystem: a correct request/response loop. Non-terminating but
// always has an enabled action, hence deadlock-free.
func pingPongSystem() *checker.System {
	a := &checker.Machine{
		Name:    "A",
		Initial: "a0",
		Transitions: []checker.Transition{
			{From: "a0", To: "a1", Act: checker.Action{Kind: checker.Send, Message: "ping"}},
			{From: "a1", To: "a0", Act: checker.Action{Kind: checker.Recv, Message: "pong"}},
		},
	}
	b := &checker.Machine{
		Name:    "B",
		Initial: "b0",
		Transitions: []checker.Transition{
			{From: "b0", To: "b1", Act: checker.Action{Kind: checker.Recv, Message: "ping"}},
			{From: "b1", To: "b0", Act: checker.Action{Kind: checker.Send, Message: "pong"}},
		},
	}
	return &checker.System{A: a, B: b}
}

func run(name string, sys *checker.System) {
	fmt.Printf("=== %s ===\n", name)
	res := sys.Explore()
	fmt.Printf("explored %d distinct global states\n", res.Explored)
	if !res.Deadlock {
		fmt.Println("no deadlock found (all non-terminating states have enabled actions)")
		fmt.Println()
		return
	}
	fmt.Printf("deadlock reached in %d step(s); shortest trace:\n", len(res.Trace))
	for i, st := range res.Trace {
		fmt.Printf("  %d. %s\n", i+1, st)
	}
	fmt.Printf("dead state: %s\n", res.DeadState)
	if err := sys.VerifyDeadlockTrace(res.Trace); err != nil {
		fmt.Printf("REPLAY FAILED: %v\n", err)
	} else {
		fmt.Println("replay check passed: every step legal, queue contents match, final state is a deadlock")
	}
	fmt.Println()
}

func main() {
	run("protocol with message mismatch (expect deadlock)", deadlockedSystem())
	run("ping-pong protocol (expect no deadlock)", pingPongSystem())
}
