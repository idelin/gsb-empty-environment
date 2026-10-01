// Demo of the acceptance scenario:
// pattern A -> B (no C in between) within a 10s sliding window.
// t=0 A, t=5 B -> hit; then a late C at t=2 arrives and the engine
// precisely retracts the previous hit and records it in the log.
package main

import (
	"fmt"

	"cepengine/cep"
)

func main() {
	engine := cep.NewEngine(10_000) // 10s sliding window
	fmt.Println("NFA-DAG graph:")
	fmt.Print(engine.GraphDescription())

	stream := []cep.Event{
		{Type: "A", Timestamp: 0},
		{Type: "B", Timestamp: 5_000},
		{Type: "C", Timestamp: 2_000}, // late / out-of-order arrival
	}

	for _, ev := range stream {
		fmt.Printf(">>> ingest %s@%dms\n", ev.Type, ev.Timestamp)
		for _, n := range engine.Process(ev) {
			fmt.Printf("    notify %-7s match #%d: A@%d -> B@%d  (%s)\n",
				n.Kind, n.Match.ID, n.Match.A.Timestamp, n.Match.B.Timestamp, n.Reason)
		}
	}

	fmt.Printf("\nactive matches after stream: %d\n", len(engine.ActiveMatches()))
	fmt.Println("\nengine log:")
	for _, line := range engine.Logs() {
		fmt.Println("  " + line)
	}
}
