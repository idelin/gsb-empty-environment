// Demo: two concurrent cross-partition transactions, out-of-order packet
// injection, deterministic convergence, and snapshot/replay recovery.
package main

import (
	"encoding/json"
	"fmt"
	"math/rand"

	sm "mpsm"
)

var cfg = sm.Config{NumPartitions: 2, MaxPending: 64, ReorderWindow: 64}

func keyForPartition(p int, prefix string) string {
	probe := sm.New(cfg)
	for i := 0; ; i++ {
		k := fmt.Sprintf("%s-%d", prefix, i)
		if probe.PartitionOf(k) == p {
			return k
		}
	}
}

func shuffled(events []sm.Event, seed int64) []sm.Event {
	out := make([]sm.Event, len(events))
	copy(out, events)
	rand.New(rand.NewSource(seed)).Shuffle(len(out), func(i, j int) {
		out[i], out[j] = out[j], out[i]
	})
	return out
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	kA := keyForPartition(0, "acct") // partition 0, contested by T1 & T2
	kB := keyForPartition(1, "acct") // partition 1, T1
	kC := keyForPartition(1, "acct") // partition 1, T2 (distinct from kB)
	if kB == kC {
		kC = keyForPartition(1, "other")
	}

	// Interleaved two-phase log: T1 and T2 span both partitions and
	// conflict on kA.
	log := []sm.Event{
		{LSN: 1, TxnID: "T1", Kind: sm.KindPrepare, Writes: map[string]string{kA: "100", kB: "50"}},
		{LSN: 2, TxnID: "T2", Kind: sm.KindPrepare, Writes: map[string]string{kA: "200", kC: "70"}},
		{LSN: 3, TxnID: "T1", Kind: sm.KindCommit},
		{LSN: 4, TxnID: "T2", Kind: sm.KindCommit},
	}
	fmt.Printf("keys: kA=%q(p0) kB=%q(p1) kC=%q(p1)\n\n", kA, kB, kC)

	// --- Part 1: out-of-order packet injection, 3 replicas ---
	fmt.Println("== replicas receiving shuffled packets ==")
	var reference string
	for seed := int64(1); seed <= 3; seed++ {
		replica := sm.New(cfg)
		must(replica.Replay(shuffled(log, seed)))
		fmt.Printf("replica(seed=%d) final state:\n%s", seed, replica.State())
		if seed == 1 {
			reference = replica.State()
			ev, _ := json.MarshalIndent(replica.Evidence(), "", "  ")
			fmt.Printf("audit evidence: %s\n\n", ev)
		} else if replica.State() != reference {
			panic("DIVERGENCE DETECTED")
		}
	}
	fmt.Println("all replicas converged to identical state")

	// --- Part 2: snapshot slice point + crash recovery ---
	fmt.Println("\n== snapshot at slice point, then replay suffix ==")
	extra := []sm.Event{
		{LSN: 5, TxnID: "T3", Kind: sm.KindPrepare, Writes: map[string]string{kB: "55"}},
		{LSN: 6, TxnID: "T3", Kind: sm.KindCommit},
	}
	full := sm.New(cfg)
	must(full.Replay(append(append([]sm.Event{}, log...), extra...)))

	crashed := sm.New(cfg)
	must(crashed.Replay(log)) // apply first 4 events
	snap := crashed.Snapshot()
	crashed.Compact(snap.ExpectLSN)
	fmt.Printf("snapshot taken at LSN %d (log compacted to %d entries)\n", snap.ExpectLSN, len(crashed.Log()))

	// Crash: discard `crashed`, recover from snapshot + log suffix.
	recovered := sm.Restore(snap, sm.Config{})
	must(recovered.Replay(shuffled(extra, 42))) // suffix arrives out of order too
	fmt.Printf("recovered state:\n%s", recovered.State())
	if recovered.State() == full.State() {
		fmt.Println("recovered state == full-replay state: OK")
	} else {
		panic("RECOVERY MISMATCH")
	}
}
