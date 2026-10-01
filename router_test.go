package dvsim

import "testing"

// twoRouters builds A--B (cost 1) without running the simulator, so tests
// can drive Receive directly.
func twoRouters(t *testing.T) (*Network, *Router, *Router) {
	t.Helper()
	sim := NewSimulator()
	net := NewNetwork(sim, 1)
	a := net.AddRouter("A")
	b := net.AddRouter("B")
	net.AddLink("A", "B", 1)
	return net, a, b
}

func TestPoisonReverse(t *testing.T) {
	_, a, b := twoRouters(t)
	// A learns "X via B" and "Y via C" (C is a fake neighbor for the test).
	a.neighbors["C"] = &neighbor{cost: 1, up: true}
	a.install("X", "B", 3)
	a.install("Y", "C", 2)

	u := a.buildUpdate("B")
	var xMetric, yMetric = -1, -1
	for _, e := range u.Entries {
		switch e.Dest {
		case "X":
			xMetric = e.Metric
		case "Y":
			yMetric = e.Metric
		}
	}
	if xMetric != Infinity {
		t.Fatalf("route via B must be poisoned back to B: got %d, want %d", xMetric, Infinity)
	}
	if yMetric != 2 {
		t.Fatalf("route via C must be advertised with real metric: got %d, want 2", yMetric)
	}
	_ = b
}

func TestStaleUpdateDropped(t *testing.T) {
	_, a, b := twoRouters(t)
	// Fresh update (seq 5) installs X with metric 4.
	b.Receive(Update{From: "A", Seq: 5, Entries: []UpdateEntry{{Dest: "X", Metric: 3}}})
	m, ok := b.Metric("X")
	if !ok || m != 4 {
		t.Fatalf("after seq5: metric=%d ok=%v, want 4", m, ok)
	}
	// Reordered older update (seq 2) claims a better metric; must be dropped.
	b.Receive(Update{From: "A", Seq: 2, Entries: []UpdateEntry{{Dest: "X", Metric: 1}}})
	m, _ = b.Metric("X")
	if m != 4 {
		t.Fatalf("stale update changed metric to %d, want 4", m)
	}
	// Newer update (seq 6) is accepted.
	b.Receive(Update{From: "A", Seq: 6, Entries: []UpdateEntry{{Dest: "X", Metric: 5}}})
	m, _ = b.Metric("X")
	if m != 6 {
		t.Fatalf("fresh update ignored: metric=%d, want 6", m)
	}
	_ = a
}

func TestSameNextHopAcceptsWorseMetric(t *testing.T) {
	_, _, b := twoRouters(t)
	b.Receive(Update{From: "A", Seq: 1, Entries: []UpdateEntry{{Dest: "X", Metric: 2}}})
	b.Receive(Update{From: "A", Seq: 2, Entries: []UpdateEntry{{Dest: "X", Metric: 9}}})
	if m, _ := b.Metric("X"); m != 10 {
		t.Fatalf("metric = %d, want 10 (worse metric from same next hop must be accepted)", m)
	}
}

func TestAbsenceMeansGone(t *testing.T) {
	_, _, b := twoRouters(t)
	b.Receive(Update{From: "A", Seq: 1, Entries: []UpdateEntry{{Dest: "X", Metric: 2}}})
	if _, ok := b.Metric("X"); !ok {
		t.Fatal("route to X should exist")
	}
	// Full-table update without X: route via A must be withdrawn.
	b.Receive(Update{From: "A", Seq: 2, Entries: []UpdateEntry{{Dest: "A", Metric: 0}}})
	if _, ok := b.Metric("X"); ok {
		t.Fatal("route to X should be withdrawn when A stops advertising it")
	}
}

func TestMetricCappedAtInfinity(t *testing.T) {
	_, _, b := twoRouters(t)
	b.Receive(Update{From: "A", Seq: 1, Entries: []UpdateEntry{{Dest: "X", Metric: Infinity}}})
	if _, ok := b.Metric("X"); ok {
		t.Fatal("metric >= Infinity must not install a route")
	}
	b.Receive(Update{From: "A", Seq: 2, Entries: []UpdateEntry{{Dest: "X", Metric: 3}}})
	b.Receive(Update{From: "A", Seq: 3, Entries: []UpdateEntry{{Dest: "X", Metric: Infinity}}})
	if _, ok := b.Metric("X"); ok {
		t.Fatal("poisoned route from same next hop must withdraw the route")
	}
}

func TestUpdateFromDownLinkIgnored(t *testing.T) {
	net, a, b := twoRouters(t)
	net.SetLink("A", "B", false)
	b.Receive(Update{From: "A", Seq: 9, Entries: []UpdateEntry{{Dest: "X", Metric: 1}}})
	if _, ok := b.Metric("X"); ok {
		t.Fatal("update over a down link must be ignored")
	}
	_ = a
}
