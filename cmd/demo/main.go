// Command demo runs the summary-tree sync protocol inside the
// single-process event simulator and prints the evidence.
package main

import (
	"fmt"
	"math/rand"

	"syncsim"
)

func main() {
	scenarioConvergence()
	scenarioBandwidth()
	scenarioDelete()
	scenarioCollision()
}

// 1. Four replicas, random concurrent writes/deletes, lossy network.
func scenarioConvergence() {
	fmt.Println("=== 1. Convergence under faults (drop=20% dup=15% delay=1..20) ===")
	s := syncsim.NewSim(42, nil)
	s.DropRate, s.DupRate = 0.20, 0.15
	s.MinDelay, s.MaxDelay = 1, 20
	nodes := []*syncsim.Node{s.AddNode("A"), s.AddNode("B"), s.AddNode("C"), s.AddNode("D")}

	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 200; i++ {
		n := nodes[rng.Intn(len(nodes))]
		k := fmt.Sprintf("k%03d", rng.Intn(60))
		if rng.Intn(4) == 0 {
			n.Store.Delete(k)
		} else {
			n.Store.Put(k, fmt.Sprintf("v%d", i))
		}
	}

	round := 0
	for {
		round++
		before := applied(nodes)
		for _, a := range nodes {
			for _, b := range nodes {
				if a != b {
					a.StartSync(b.ID)
				}
			}
		}
		s.Run()
		if applied(nodes) == before {
			break
		}
	}
	fmt.Printf("fixpoint reached after %d rounds (sim time %d ticks)\n", round, s.Now)
	fmt.Printf("messages: sent=%d dropped=%d duplicated=%d, records applied=%d\n",
		s.Sent, s.Dropped, s.Duplicated, applied(nodes))
	fmt.Printf("converged: %v (all replicas identical, tombstones included)\n\n", converged(nodes))
}

// 2. 5000 identical keys + 1 difference: only the differing leaf moves.
func scenarioBandwidth() {
	fmt.Println("=== 2. Identical subtrees are not transmitted in full ===")
	s := syncsim.NewSim(3, nil)
	a, b := s.AddNode("A"), s.AddNode("B")
	for i := 0; i < 5000; i++ {
		r := a.Store.Put(fmt.Sprintf("key-%05d", i), "shared")
		b.Store.Apply(r)
	}
	a.Store.Put("fresh-key", "only-on-A")
	s.Sent = 0
	resetStats(nodes(a, b))

	a.StartSync("B")
	b.StartSync("A")
	s.Run()
	fmt.Printf("state size: 5001 records; records transmitted: %d; messages: %d\n",
		sentRecords(nodes(a, b)), s.Sent)
	fmt.Printf("converged: %v\n\n", converged(nodes(a, b)))
}

// 3. Delete propagates as a tombstone; a stale value cannot resurrect it.
func scenarioDelete() {
	fmt.Println("=== 3. Deletes propagate and never resurrect ===")
	s := syncsim.NewSim(2, nil)
	a, b := s.AddNode("A"), s.AddNode("B")
	a.Store.Put("k", "v1")
	a.StartSync("B")
	s.Run()
	v, _ := b.Store.LiveValue("k")
	fmt.Printf("after put+sync:    B sees k=%q\n", v)

	a.Store.Delete("k")
	a.StartSync("B")
	s.Run()
	_, live := b.Store.LiveValue("k")
	fmt.Printf("after delete+sync: B live=%v (tombstone replicated)\n", live)

	stale := syncsim.Record{Key: "k", Value: "v1", Ver: syncsim.Version{Clock: 1, Node: "A"}}
	changed := b.Store.Apply(stale)
	_, live = b.Store.LiveValue("k")
	fmt.Printf("stale v1 arrives late: applied=%v, B live=%v (old value loses to tombstone)\n\n", changed, live)
}

// 4. Forced summary collision: without review the divergence is hidden;
// with the audit review path it is detected and repaired.
func scenarioCollision() {
	fmt.Println("=== 4. Forced summary collision and the review path ===")
	weak := func(b []byte) uint64 { return uint64(len(b)) } // colliding hash

	build := func(audit float64) (*syncsim.Sim, *syncsim.Node, *syncsim.Node) {
		s := syncsim.NewSim(4, weak)
		a, b := s.AddNode("A"), s.AddNode("B")
		a.Store.Put("k", "alpha")
		b.Store.Put("k", "zzzzz")
		b.Store.Put("k", "gamma") // highest version, must win
		a.AuditRate, b.AuditRate = audit, audit
		return s, a, b
	}

	s1, a1, b1 := build(0)
	a1.StartSync("B")
	b1.StartSync("A")
	s1.Run()
	va, _ := a1.Store.LiveValue("k")
	vb, _ := b1.Store.LiveValue("k")
	fmt.Printf("audit off: roots collide -> no traffic; A=%q B=%q (diverged)\n", va, vb)

	s2, a2, b2 := build(1.0)
	a2.StartSync("B")
	b2.StartSync("A")
	s2.Run()
	va, _ = a2.Store.LiveValue("k")
	vb, _ = b2.Store.LiveValue("k")
	fmt.Printf("audit on:  matched digests re-checked -> A=%q B=%q (converged)\n", va, vb)
}

func nodes(ns ...*syncsim.Node) []*syncsim.Node { return ns }

func applied(ns []*syncsim.Node) int {
	t := 0
	for _, n := range ns {
		t += n.Stats.Applied
	}
	return t
}

func sentRecords(ns []*syncsim.Node) int {
	t := 0
	for _, n := range ns {
		t += n.Stats.RecordsSent
	}
	return t
}

func resetStats(ns []*syncsim.Node) {
	for _, n := range ns {
		n.Stats = syncsim.Stats{}
	}
}

func converged(ns []*syncsim.Node) bool {
	first := ns[0].Store.Records()
	for _, n := range ns[1:] {
		other := n.Store.Records()
		if len(other) != len(first) {
			return false
		}
		for k, r := range first {
			if o, ok := other[k]; !ok || o != r {
				return false
			}
		}
	}
	return true
}
