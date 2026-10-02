// Command joindemo replays the acceptance scenario against a deterministic
// manual clock and prints every join result as it is emitted.
package main

import (
	"fmt"
	"time"

	"streamjoin"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

func main() {
	clock := streamjoin.NewManualClock(t0)
	joiner := streamjoin.NewJoiner(5*time.Second, 10*time.Second, clock, func(r streamjoin.Result) {
		switch r.Kind {
		case streamjoin.Paired:
			note := ""
			if r.Late {
				note = " (late compensation)"
			}
			fmt.Printf("[t=%2ds] PAIRED    id=%-2s left@t=%ds right@t=%ds%s\n",
				int(clock.Now().Sub(t0).Seconds()), r.ID,
				int(r.Left.At.Sub(t0).Seconds()), int(r.Right.At.Sub(t0).Seconds()), note)
		case streamjoin.LeftOnly:
			fmt.Printf("[t=%2ds] LEFT-ONLY id=%-2s left@t=%ds (timeout, no match)\n",
				int(clock.Now().Sub(t0).Seconds()), r.ID,
				int(r.Left.At.Sub(t0).Seconds()))
		}
	})

	send := func(sec int, ev streamjoin.Event) {
		clock.Advance(at(sec))
		fmt.Printf("--- t=%2ds  input %-6s id=%s (event time t=%ds)\n",
			sec, ev.Kind, ev.ID, int(ev.At.Sub(t0).Seconds()))
		joiner.Handle(ev)
	}

	fmt.Println("== Scenario 1: L1@t=0, R1@t=3 -> immediate pair ==")
	send(0, streamjoin.Event{Kind: streamjoin.LeftEvent, ID: "L1", At: at(0)})
	send(3, streamjoin.Event{Kind: streamjoin.RightEvent, ID: "L1", At: at(3)})

	fmt.Println("\n== Scenario 2: L2@t=10, cancel@t=12 -> no orphan output at t=15 ==")
	send(10, streamjoin.Event{Kind: streamjoin.LeftEvent, ID: "L2", At: at(10)})
	send(12, streamjoin.Event{Kind: streamjoin.CancelEvent, ID: "L2", At: at(12)})
	clock.Advance(at(15))
	fmt.Println("--- t=15s  (L2 deadline passed silently)")

	fmt.Println("\n== Scenario 3: L3@t=20 times out, late R3(event t=22)@t=27 -> compensation ==")
	send(20, streamjoin.Event{Kind: streamjoin.LeftEvent, ID: "L3", At: at(20)})
	clock.Advance(at(26)) // t=25 deadline fires -> LEFT-ONLY
	send(27, streamjoin.Event{Kind: streamjoin.RightEvent, ID: "L3", At: at(22)})

	fmt.Println("\n== Scenario 4: out-of-order R4@t=30 before L4(event t=28)@t=32 ==")
	send(30, streamjoin.Event{Kind: streamjoin.RightEvent, ID: "L4", At: at(31)})
	send(32, streamjoin.Event{Kind: streamjoin.LeftEvent, ID: "L4", At: at(28)})

	clock.Advance(at(120))
	fmt.Printf("\nfinal state: %+v (all zero = no leak), pending timers: %d\n",
		joiner.Stats(), clock.PendingTimers())
}
