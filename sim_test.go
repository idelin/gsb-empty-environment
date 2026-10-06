package main

import "testing"

func TestChainLatencies(t *testing.T) {
	sim, err := NewSim(chainConfig())
	if err != nil {
		t.Fatal(err)
	}
	res := sim.Run(1000)
	if res.Deadlock {
		t.Fatalf("unexpected deadlock at tick %d", res.Ticks)
	}
	if !sim.Done() {
		t.Fatalf("not all packets delivered after %d ticks", res.Ticks)
	}
	want := map[int]int{0: 3, 1: 4, 2: 3, 3: 2}
	for id, lat := range want {
		if got := sim.Latency(id); got != lat {
			t.Errorf("packet %d latency = %d, want %d", id, got, lat)
		}
	}
}

func TestDeterministicAcrossRuns(t *testing.T) {
	var runs [][]int
	var lats [][]int
	for i := 0; i < 5; i++ {
		sim, err := NewSim(chainConfig())
		if err != nil {
			t.Fatal(err)
		}
		sim.Run(1000)
		runs = append(runs, sim.Delivered())
		lat := make([]int, 4)
		for id := range lat {
			lat[id] = sim.Latency(id)
		}
		lats = append(lats, lat)
	}
	for i := 1; i < 5; i++ {
		for j := range runs[0] {
			if runs[i][j] != runs[0][j] || lats[i][j] != lats[0][j] {
				t.Fatalf("run %d differs from run 0: %v/%v vs %v/%v",
					i, runs[i], lats[i], runs[0], lats[0])
			}
		}
	}
}

func TestRingDeadlockEvidence(t *testing.T) {
	sim, err := NewSim(ringConfig(3, 2))
	if err != nil {
		t.Fatal(err)
	}
	res := sim.Run(1000)
	if !res.Deadlock {
		t.Fatal("expected deadlock on saturated 3-ring")
	}
	if len(res.DeadlockE) != 3 {
		t.Fatalf("wait-for cycle length = %d, want 3: %v", len(res.DeadlockE), res.DeadlockE)
	}
	// The cycle must visit each node exactly once and close on itself.
	seen := map[int]bool{}
	for i, we := range res.DeadlockE {
		if seen[we.From] {
			t.Fatalf("node %d appears twice in cycle", we.From)
		}
		seen[we.From] = true
		if we.To != res.DeadlockE[(i+1)%len(res.DeadlockE)].From {
			t.Fatalf("cycle is not contiguous at edge %d: %v", i, res.DeadlockE)
		}
	}
	if sim.Done() {
		t.Fatal("deadlocked sim reports all delivered")
	}
}

func TestBlockingSendNeverDrops(t *testing.T) {
	// Two packets, one-capacity edges: the second send must block and
	// retry, not drop. Both packets must eventually arrive.
	cfg := twoNodeConfig(1)
	cfg.Traffic = append(cfg.Traffic, Packet{ID: 2, Src: 0, Dst: 1, Born: 0})
	sim, err := NewSim(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res := sim.Run(1000)
	if res.Deadlock || !sim.Done() {
		t.Fatalf("expected full delivery, got deadlock=%v done=%v", res.Deadlock, sim.Done())
	}
	for id := 0; id < 3; id++ {
		if sim.Latency(id) < 0 {
			t.Fatalf("packet %d lost", id)
		}
	}
}

func TestNoRouteRejected(t *testing.T) {
	cfg := twoNodeConfig(1)
	cfg.NextHop[0][1] = -1
	if _, err := NewSim(cfg); err == nil {
		t.Fatal("expected error for missing route")
	}
}
