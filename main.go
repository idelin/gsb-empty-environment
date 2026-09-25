// Command snaprepl runs a sample scenario in the event simulator:
// a lagging follower installs a chunked snapshot (with loss, duplication
// and reordering), rolls back one corrupted attempt, then catches up on
// the remaining log. Safety and liveness evidence is printed at the end.
package main

import (
	"fmt"
	"sort"

	"snaprepl/repl"
	"snaprepl/sim"
)

func main() {
	eng := sim.NewEngine()
	eng.Trace = true
	net := sim.NewNet(eng, 42)
	net.MinDelay, net.MaxDelay = 1, 8
	net.DropProb = 0.15 // flaky network: loss...
	net.DupProb = 0.15  // ...duplication and (via random delay) reordering

	leader := repl.NewLeader("leader", net, 24)
	follower := repl.NewFollower("follower", net)
	leader.Peers = []string{"follower"}

	// Safety evidence: check the prefix invariant after EVERY event.
	checker := repl.NewChecker(leader, follower)
	eng.OnStep = checker.Check

	// t=0..19: leader commits 12 entries; follower is partitioned away
	// (messages dropped by the filter), so it lags.
	eng.After(0, "phase1: leader commits while follower partitioned", func() {
		net.Filter = func(from, to string, msg interface{}) (interface{}, bool) {
			return msg, to == "follower" // total partition
		}
		for i := 1; i <= 12; i++ {
			leader.Propose(fmt.Sprintf("k%02d", i), fmt.Sprintf("v%d", i))
		}
	})

	// t=20: partition heals. Follower is too far behind: leader sends a
	// chunked snapshot of entries 1..10, then keeps appending entries
	// 13..15 which interleave with the chunks in flight.
	eng.After(20, "phase2: heal, send snapshot@10 + entries 13-15", func() {
		net.Filter = nil
		leader.SendSnapshot("follower", 10)
		for i := 13; i <= 15; i++ {
			leader.Propose(fmt.Sprintf("k%02d", i), fmt.Sprintf("v%d", i))
		}
	})

	// t=60: a corrupted snapshot attempt (bit flip in a chunk) must be
	// rolled back without touching the applied state.
	eng.After(60, "phase3: corrupted snapshot attempt", func() {
		net.DropProb = 0 // make sure the corrupted chunk is delivered
		corrupted := false
		net.Filter = func(from, to string, msg interface{}) (interface{}, bool) {
			if c, ok := msg.(repl.Chunk); ok && !corrupted && c.Offset == 1 {
				corrupted = true
				bad := append([]byte(nil), c.Data...)
				bad[0] ^= 0xff
				c.Data = bad
				return c, false
			}
			return msg, false
		}
		leader.SendSnapshot("follower", 12)
	})
	eng.After(80, "phase3b: clean retry after rollback", func() {
		net.Filter = nil
		net.DropProb = 0.15
		leader.SendSnapshot("follower", 12)
	})

	// t=120: follower crashes mid-install; volatile staging is lost,
	// durable state survives; leader re-sends and follower recovers.
	eng.After(120, "phase4: crash mid-install, resend, recover", func() {
		leader.SendSnapshot("follower", 15)
		eng.After(2, "follower crashes", func() { follower.Crash() })
		eng.After(10, "leader re-sends snapshot", func() {
			leader.SendSnapshot("follower", 15)
		})
	})

	eng.Run()

	fmt.Println("=== event trace (first 30) ===")
	for i, l := range eng.TraceL {
		if i >= 30 {
			fmt.Printf("... (%d more events)\n", len(eng.TraceL)-30)
			break
		}
		fmt.Println(l)
	}

	state, last, gen := follower.ReadAll()
	fmt.Println("\n=== final follower state ===")
	keys := make([]string, 0, len(state))
	for k := range state {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("%s = %s\n", k, state[k])
	}

	fmt.Println("\n=== evidence ===")
	fmt.Printf("SAFETY: prefix invariant checked after every event: %d checks, violations=%v\n",
		checker.Checks, checker.Violations)
	fmt.Printf("LIVENESS: follower lastApplied=%d, leader commitIndex=%d, caughtUp=%v (at virtual t=%d)\n",
		last, leader.CommitIndex(), last == leader.CommitIndex(), eng.Now())
	fmt.Printf("counters: %s\n", follower.Stats)
	fmt.Printf("network: sent=%d delivered=%d dropped=%d duplicated=%d\n",
		net.Sent, net.Delivered, net.Dropped, net.Duplicated)
	fmt.Printf("snapshot generation installed: %d\n", gen)
}
