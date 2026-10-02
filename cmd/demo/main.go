// Command demo runs the acceptance scenario: at most 3 requests per 10s,
// events t=1,2,8 pass, then a late event at t=0 arrives and forces the
// revocation of the previously accepted t=8.
package main

import (
	"fmt"

	"eventlimiter"
)

func main() {
	l := eventlimiter.New(3, 10)
	fmt.Println("rule: at most 3 requests per 10s sliding window (event time)")

	submit := func(id string, t int64) {
		fmt.Printf("\n--> submit %s (event time t=%d)\n", id, t)
		for _, d := range l.Submit(eventlimiter.Event{ID: id, Time: t}) {
			fmt.Printf("    decision: %s\n", d)
		}
	}

	submit("req-A", 1)
	submit("req-B", 2)
	submit("req-C", 8)
	submit("req-D", 0) // late arrival: occurred before all others

	fmt.Println("\nfinal state (event-time order):")
	for _, e := range l.Events() {
		status := "REJECTED"
		if l.Allowed(e.ID) {
			status = "ALLOWED "
		}
		fmt.Printf("    t=%-2d %s %s\n", e.Time, e.ID, status)
	}
}
