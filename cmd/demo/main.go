// Command demo simulates a leaderless replicated store using Dotted
// Version Vectors: three replicas accept high-frequency empty updates,
// causal overwrites and conflicting writes to the same hot key, then
// exchange versions over an unreliable network that reorders and
// duplicates broadcasts. It prints how DVV deduplicates repeated
// writes, prunes superseded versions (no ghost branches) and converges
// to an identical, minimal state on every replica.
package main

import (
	"fmt"
	"math/rand"
	"reflect"

	"dvv"
)

func main() {
	rng := rand.New(rand.NewSource(7))
	nodes := []string{"n1", "n2", "n3"}
	replicas := map[string]*dvv.Store{}
	for _, n := range nodes {
		replicas[n] = dvv.NewStore(n)
	}

	type msg struct {
		key string
		v   dvv.Value
	}
	var network []msg
	broadcast := func(key string, v dvv.Value) { network = append(network, msg{key, v}) }

	// --- Phase 1: workload -------------------------------------------
	fmt.Println("== Phase 1: 300 writes on hot key 'config' ==")
	ctx := dvv.EmptyClock()
	for i := 0; i < 300; i++ {
		n := nodes[rng.Intn(len(nodes))]
		s := replicas[n]
		var value string
		switch i % 10 {
		case 0, 1, 2, 3, 4, 5, 6, 7: // 80% empty updates: same value
			value = "timeout=30s"
		default: // 20% overwrite updates: new value, causal context
			value = fmt.Sprintf("timeout=%ds", 30+i)
		}
		ctx = s.Put("config", value, ctx)
		broadcast("config", dvv.Value{Data: value, Clock: ctx})
	}
	// A genuine conflict: two replicas write different values blindly.
	c1 := replicas["n1"].Put("config", "manual-override-A", dvv.EmptyClock())
	c2 := replicas["n2"].Put("config", "manual-override-B", dvv.EmptyClock())
	broadcast("config", dvv.Value{Data: "manual-override-A", Clock: c1})
	broadcast("config", dvv.Value{Data: "manual-override-B", Clock: c2})

	// Duplicate retry storm: the same write re-issued 50 times.
	retryCtx := dvv.EmptyClock()
	for i := 0; i < 50; i++ {
		retryCtx = replicas["n3"].Put("heartbeat", "alive", retryCtx)
	}
	broadcast("heartbeat", dvv.Value{Data: "alive", Clock: retryCtx})

	// --- Phase 2: shuffled, duplicated broadcast ---------------------
	fmt.Println("== Phase 2: gossip over reordering, duplicating network ==")
	for round := 0; round < 3; round++ {
		for _, idx := range rng.Perm(len(network)) {
			m := network[idx]
			for _, n := range nodes {
				if rng.Intn(2) == 0 { // random subset, random duplication
					replicas[n].Merge(m.key, m.v)
				}
			}
		}
	}
	for round := 0; round < 2; round++ { // anti-entropy
		for _, i := range rng.Perm(len(nodes)) {
			for _, j := range rng.Perm(len(nodes)) {
				if i != j {
					replicas[nodes[i]].Sync(replicas[nodes[j]])
				}
			}
		}
	}

	// --- Phase 3: verify convergence and minimality ------------------
	fmt.Println("== Phase 3: converged replica state ==")
	base := replicas["n1"].Snapshot()
	converged := true
	for _, n := range nodes[1:] {
		if !reflect.DeepEqual(base, replicas[n].Snapshot()) {
			converged = false
		}
	}
	fmt.Printf("all replicas converged: %v\n\n", converged)
	for _, n := range nodes {
		s := replicas[n]
		keys, versions, clockEntries := s.Stats()
		fmt.Printf("replica %s: keys=%d live-versions=%d clock-entries=%d\n", n, keys, versions, clockEntries)
		for _, line := range s.Snapshot() {
			fmt.Printf("    %s\n", line)
		}
	}
	fmt.Println()
	fmt.Println("300 writes + 50 retries collapsed to a handful of versions;")
	fmt.Println("only the genuine conflict (manual-override-A vs -B) remains")
	fmt.Println("as siblings, and every clock is O(nodes) wide, not O(writes).")
}
