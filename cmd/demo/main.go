// Command demo runs a 3-node cluster with concurrent transfers, initiates a
// Chandy-Lamport snapshot from node A, and prints the resulting consistent
// cut together with the money-conservation check.
package main

import (
	"fmt"
	"time"

	"chandylamport"
)

func main() {
	balances := map[string]int{"A": 100, "B": 200, "C": 300}
	total := 0
	for _, b := range balances {
		total += b
	}
	fmt.Printf("initial balances: A=%d B=%d C=%d (total=%d)\n",
		balances["A"], balances["B"], balances["C"], total)

	collector := make(chan snapshot.SnapshotResult, len(balances))
	procs := snapshot.NewCluster(balances, collector)
	for _, p := range procs {
		p.Start()
		defer p.Stop()
	}

	fmt.Println("concurrent transfers running...")
	time.Sleep(200 * time.Millisecond)

	fmt.Println("node A initiates snapshot (markers flowing on all channels)")
	procs[0].InitiateSnapshot(1)

	results := make(map[string]snapshot.SnapshotResult, len(procs))
	for len(results) < len(procs) {
		r := <-collector
		results[r.ProcessID] = r
	}

	sum := 0
	for _, id := range []string{"A", "B", "C"} {
		r := results[id]
		fmt.Printf("node %s: local balance=%d, in-flight=%d msg(s) totaling %d\n",
			id, r.Balance, len(flatten(r.InFlight)), r.InFlightTotal())
		for from, msgs := range r.InFlight {
			for _, m := range msgs {
				fmt.Printf("  channel %s->%s: in-flight transfer amount=%d\n", from, id, m.Amount)
			}
		}
		sum += r.Balance + r.InFlightTotal()
	}

	fmt.Printf("snapshot total (local states + in-flight) = %d, initial total = %d\n", sum, total)
	if sum != total {
		fmt.Println("FAIL: money not conserved")
	} else {
		fmt.Println("OK: total money strictly conserved across the consistent cut")
	}
}

func flatten(m map[string][]snapshot.Message) []snapshot.Message {
	var out []snapshot.Message
	for _, msgs := range m {
		out = append(out, msgs...)
	}
	return out
}
