package main

import (
	"fmt"
	"os"

	"deadlock/checker"
)

// crossingProtocol: both machines send first, then wait for a specific reply
// that never matches the queued message head -> reachable deadlock.
func crossingProtocol() [2]checker.Machine {
	a := checker.Machine{
		Name: "A",
		Trans: map[int][]checker.Transition{
			0: {{Kind: checker.Send, Msg: "a1", To: 1}},
			1: {{Kind: checker.Recv, Msg: "b", To: 2}},
		},
		Finals: map[int]bool{2: true},
	}
	b := checker.Machine{
		Name: "B",
		Trans: map[int][]checker.Transition{
			0: {{Kind: checker.Send, Msg: "b1", To: 1}},
			1: {{Kind: checker.Recv, Msg: "a", To: 2}},
		},
		Finals: map[int]bool{2: true},
	}
	return [2]checker.Machine{a, b}
}

func main() {
	m := crossingProtocol()
	steps, dl, found := checker.FindDeadlock(m)
	if !found {
		fmt.Println("no reachable deadlock")
		return
	}
	fmt.Printf("deadlock reachable in %d steps:\n", len(steps))
	for i, st := range steps {
		fmt.Printf("  %d: %s\n", i, st)
	}
	fmt.Printf("deadlock state: %s\n", dl)

	final, err := checker.CheckDeadlockTrace(m, steps)
	if err != nil {
		fmt.Println("trace replay FAILED:", err)
		os.Exit(1)
	}
	fmt.Println("trace replay OK, verified deadlock at", final)
}
