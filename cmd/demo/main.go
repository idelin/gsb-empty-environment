// Demo of the acceptance scenario: A@0, B@5 -> hit; late C@2 -> retraction.
package main

import (
	"fmt"

	"cep/cep"
)

func main() {
	eng := cep.NewEngine(10) // pattern: A -> B within 10s, no C between

	stream := []cep.Event{
		{Type: "A", Time: 0},
		{Type: "B", Time: 5},
		{Type: "C", Time: 2}, // late, out-of-order arrival
	}

	fmt.Println("== input stream (arrival order) ==")
	for _, ev := range stream {
		fmt.Printf("arrive: %s@%d\n", ev.Type, ev.Time)
		eng.Process(ev)
	}

	fmt.Println("\n== notifications ==")
	for _, n := range eng.Notifications() {
		fmt.Printf("[%s] A@%d -> B@%d (emitted at t=%d, caused by %s@%d)\n",
			n.Kind, n.Match.A.Time, n.Match.B.Time, n.EmittedAt, n.CausedBy.Type, n.CausedBy.Time)
	}

	fmt.Println("\n== engine log ==")
	for _, line := range eng.Log() {
		fmt.Println(line)
	}
}
