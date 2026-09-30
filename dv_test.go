package dvsim

import (
	"math/rand"
	"testing"
)

// checkConverged asserts the event queue drained (no permanent
// count-to-infinity) and every table matches reference shortest paths.
func checkConverged(t *testing.T, sim *Sim) {
	t.Helper()
	if p := sim.Pending(); p != 0 {
		t.Fatalf("simulation did not converge: %d events still pending at tick %d", p, sim.Now)
	}
	ref := sim.ReferenceDistances()
	for _, src := range sim.NodeIDs() {
		for _, dst := range sim.NodeIDs() {
			want := Infinity
			if d, ok := ref[src][dst]; ok && d < Infinity {
				want = d
			}
			if got := sim.Node(src).Metric(dst); got != want {
				t.Errorf("node %d -> %d: metric %d, want %d", src, dst, got, want)
			}
		}
	}
}

// diamond: 0-1-3 and 0-2-3, plus a tail 3-4.
func diamond() (*Sim, map[[2]int]*Link) {
	sim := NewSim(1)
	for id := 0; id < 5; id++ {
		sim.AddNode(id)
	}
	links := map[[2]int]*Link{}
	add := func(a, b, c int) { links[[2]int{a, b}] = sim.AddLink(a, b, c) }
	add(0, 1, 1)
	add(0, 2, 1)
	add(1, 3, 1)
	add(2, 3, 5)
	add(3, 4, 1)
	add(1, 2, 2)
	return sim, links
}

func TestConvergesToShortestPaths(t *testing.T) {
	sim, _ := diamond()
	sim.Start()
	sim.Run(100000)
	checkConverged(t, sim)
}

func TestLinkFailureReroutes(t *testing.T) {
	sim, links := diamond()
	sim.Start()
	sim.Run(100000)
	checkConverged(t, sim)

	sim.SetLinkUp(links[[2]int{1, 3}], false) // best path to 3 and 4 breaks
	sim.Run(100000)
	checkConverged(t, sim)

	if got := sim.Node(0).Metric(4); got != 7 { // 0-2-3-4 = 1+5+1
		t.Fatalf("node 0 -> 4 after failure: got %d, want 7", got)
	}
}

func TestUnreachableDestination(t *testing.T) {
	sim, links := diamond()
	sim.Start()
	sim.Run(100000)

	sim.SetLinkUp(links[[2]int{3, 4}], false) // isolate node 4
	sim.Run(100000)
	checkConverged(t, sim)

	for _, id := range []int{0, 1, 2, 3} {
		if got := sim.Node(id).Metric(4); got != Infinity {
			t.Fatalf("node %d -> isolated 4: got %d, want Infinity", id, got)
		}
	}
}

// TestCountToInfinityTerminates builds a triangle behind the failed link,
// the classic setup for count-to-infinity, and asserts the metric ceiling
// plus poison reverse make it terminate instead of looping forever.
func TestCountToInfinityTerminates(t *testing.T) {
	sim := NewSim(7)
	for id := 0; id < 4; id++ {
		sim.AddNode(id)
	}
	l01 := sim.AddLink(0, 1, 1)
	sim.AddLink(1, 2, 1)
	sim.AddLink(2, 3, 1)
	sim.AddLink(1, 3, 1)
	sim.Start()
	sim.Run(100000)
	checkConverged(t, sim)

	sim.SetLinkUp(l01, false) // node 0 becomes unreachable
	sim.Run(100000)
	checkConverged(t, sim) // fails if count-to-infinity never ends
	for _, id := range []int{1, 2, 3} {
		if got := sim.Node(id).Metric(0); got != Infinity {
			t.Fatalf("node %d -> 0: got %d, want Infinity", id, got)
		}
	}
}

func TestLinkRecovery(t *testing.T) {
	sim, links := diamond()
	sim.Start()
	sim.Run(100000)
	checkConverged(t, sim)

	l := links[[2]int{1, 3}]
	sim.SetLinkUp(l, false)
	sim.Run(100000)
	checkConverged(t, sim)

	sim.SetLinkUp(l, true)
	sim.Run(100000)
	checkConverged(t, sim)
	if got := sim.Node(0).Metric(4); got != 3 {
		t.Fatalf("node 0 -> 4 after recovery: got %d, want 3", got)
	}
}

// TestRandomTopologiesAndEvents stresses reordering (random latencies)
// and random link toggles across many seeds.
func TestRandomTopologiesAndEvents(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		sim := NewSim(seed)
		const n = 6
		for id := 0; id < n; id++ {
			sim.AddNode(id)
		}
		var links []*Link
		for i := 0; i < n; i++ { // ring keeps it connected initially
			links = append(links, sim.AddLink(i, (i+1)%n, 1+rng.Intn(4)))
		}
		for i := 0; i < n; i++ { // random chords
			if j := rng.Intn(n); i != j && rng.Intn(2) == 0 {
				links = append(links, sim.AddLink(i, j, 1+rng.Intn(4)))
			}
		}
		sim.Start()
		for k := 0; k < 8; k++ { // random link toggles at random ticks
			l := links[rng.Intn(len(links))]
			tick := rng.Intn(200)
			sim.Schedule(tick, func() { sim.SetLinkUp(l, !l.Up) })
		}
		sim.Run(100000)
		checkConverged(t, sim)
	}
}

func TestPoisonReverse(t *testing.T) {
	sim := NewSim(0)
	a := sim.AddNode(0)
	sim.AddNode(1)
	sim.AddNode(2)
	sim.AddLink(0, 1, 1)
	sim.AddLink(0, 2, 1)
	// route to 9 learned via neighbor 1
	a.table[9] = Route{Metric: 3, NextHop: 1, Version: 4}

	for _, e := range a.advertisement(1) {
		if e.Dest == 9 && e.Metric != Infinity {
			t.Fatalf("route via neighbor 1 advertised to 1 with metric %d, want Infinity (poison reverse)", e.Metric)
		}
	}
	found := false
	for _, e := range a.advertisement(2) {
		if e.Dest == 9 {
			found = true
			if e.Metric != 3 {
				t.Fatalf("route via 1 advertised to 2 with metric %d, want 3", e.Metric)
			}
		}
	}
	if !found {
		t.Fatal("route to 9 missing from advertisement to neighbor 2")
	}
}

func TestOutOfOrderMessagesDropped(t *testing.T) {
	sim := NewSim(0)
	a := sim.AddNode(0)
	b := sim.AddNode(1)
	sim.AddLink(0, 1, 1)

	b.receive(Message{From: 0, Seq: 2, Entries: []Entry{{Dest: 5, Metric: 4, Version: 1}}})
	b.receive(Message{From: 0, Seq: 1, Entries: []Entry{{Dest: 5, Metric: 1, Version: 1}}}) // stale, must be dropped
	if got := b.Metric(5); got != 5 {
		t.Fatalf("out-of-order message overwrote route: metric %d, want 5", got)
	}
	b.receive(Message{From: 0, Seq: 2, Entries: []Entry{{Dest: 5, Metric: 1, Version: 1}}}) // duplicate
	if got := b.Metric(5); got != 5 {
		t.Fatalf("duplicate message overwrote route: metric %d, want 5", got)
	}
	_ = a
}

func TestVersionRules(t *testing.T) {
	sim := NewSim(0)
	sim.AddNode(0)
	b := sim.AddNode(1)
	sim.AddLink(0, 1, 1)

	b.processEntry(0, 1, Entry{Dest: 7, Metric: 2, Version: 3})
	if got := b.Metric(7); got != 3 {
		t.Fatalf("new route: got %d, want 3", got)
	}
	// older version must be ignored even with a better metric
	b.processEntry(0, 1, Entry{Dest: 7, Metric: 0, Version: 2})
	if got := b.Metric(7); got != 3 {
		t.Fatalf("stale version accepted: got %d, want 3", got)
	}
	// newer version always wins, even with a worse metric
	b.processEntry(0, 1, Entry{Dest: 7, Metric: 5, Version: 4})
	if got := b.Metric(7); got != 6 {
		t.Fatalf("newer version rejected: got %d, want 6", got)
	}
	// same version, better metric from another neighbor wins
	b.processEntry(9, 1, Entry{Dest: 7, Metric: 1, Version: 4})
	if got := b.Metric(7); got != 2 {
		t.Fatalf("better same-version metric rejected: got %d, want 2", got)
	}
	// same version, worse metric from the current next hop is accepted
	// (bad news travels through the next hop)
	b.processEntry(9, 1, Entry{Dest: 7, Metric: 10, Version: 4})
	if got := b.Metric(7); got != 11 {
		t.Fatalf("bad news from next hop rejected: got %d, want 11", got)
	}
	// same version, worse metric from a non-next-hop is ignored
	b.processEntry(0, 1, Entry{Dest: 7, Metric: 15, Version: 4})
	if got := b.Metric(7); got != 11 {
		t.Fatalf("worse metric from non-next-hop accepted: got %d, want 11", got)
	}
}

func TestMessageLostOnDownLink(t *testing.T) {
	sim := NewSim(0)
	sim.AddNode(0)
	b := sim.AddNode(1)
	l := sim.AddLink(0, 1, 1)
	sim.SetLinkUp(l, false)
	b.receive(Message{From: 0, Seq: 1, Entries: []Entry{{Dest: 5, Metric: 1, Version: 1}}})
	if got := b.Metric(5); got != Infinity {
		t.Fatalf("message on down link accepted: metric %d, want Infinity", got)
	}
}
