package dvsim

import (
	"math/rand"
	"testing"
)

// sampleNet builds the 6-router topology used by the demo.
func sampleNet(seed int64) (*Simulator, *Network) {
	sim := NewSimulator()
	net := NewNetwork(sim, seed)
	for _, id := range []string{"A", "B", "C", "D", "E", "F"} {
		net.AddRouter(id)
	}
	net.AddLink("A", "B", 1)
	net.AddLink("B", "C", 1)
	net.AddLink("A", "D", 2)
	net.AddLink("B", "E", 3)
	net.AddLink("C", "F", 1)
	net.AddLink("D", "E", 1)
	net.AddLink("E", "F", 1)
	return sim, net
}

func assertConverged(t *testing.T, net *Network, phase string) {
	t.Helper()
	if problems := VerifyAgainstReference(net); len(problems) > 0 {
		t.Fatalf("%s: not converged to reference shortest paths:\n%v", phase, problems)
	}
}

func TestConvergesToReference(t *testing.T) {
	sim, net := sampleNet(7)
	sim.Run(300)
	assertConverged(t, net, "initial")
}

func TestLinkFailureRecoversBounded(t *testing.T) {
	sim, net := sampleNet(7)
	sim.Run(300)
	assertConverged(t, net, "initial")

	downTick := sim.Now()
	net.SetLink("B", "C", false)
	sim.Run(downTick + 400)
	assertConverged(t, net, "after link down")

	// Count-to-infinity must be bounded: convergence well before the cap
	// would even matter, and no finite metric may ever reach Infinity.
	if got := net.ConvergedTick() - downTick; got > 150 {
		t.Fatalf("reconvergence took %d ticks after failure, want <= 150", got)
	}
	for _, id := range net.RouterIDs() {
		if m := net.Router(id).MaxMetricSeen(); m >= Infinity {
			t.Fatalf("router %s installed metric %d >= Infinity: unbounded count-to-infinity", id, m)
		}
	}

	net.SetLink("B", "C", true)
	sim.Run(sim.Now() + 300)
	assertConverged(t, net, "after link restore")
}

func TestPartitionUnreachableNotInfinite(t *testing.T) {
	sim, net := sampleNet(7)
	sim.Run(300)

	// Cut C and F off completely.
	net.SetLink("B", "C", false)
	net.SetLink("E", "F", false)
	sim.Run(sim.Now() + 1000)

	ref := ReferenceDistances(net)
	for _, id := range net.RouterIDs() {
		r := net.Router(id)
		for _, dest := range net.RouterIDs() {
			m, ok := r.Metric(dest)
			if ok && m >= Infinity {
				t.Fatalf("router %s holds infinite metric to %s", id, dest)
			}
			if ref[id][dest] >= Infinity && ok {
				t.Fatalf("router %s still has route to partitioned %s (metric %d)", id, dest, m)
			}
		}
	}
	assertConverged(t, net, "partitioned")
}

func TestRandomTopologiesConverge(t *testing.T) {
	for seed := int64(1); seed <= 8; seed++ {
		rng := rand.New(rand.NewSource(seed))
		sim := NewSimulator()
		net := NewNetwork(sim, seed)
		ids := []string{"n0", "n1", "n2", "n3", "n4", "n5", "n6", "n7"}
		for _, id := range ids {
			net.AddRouter(id)
		}
		// Spanning tree to guarantee connectivity, then extra random links.
		for i := 1; i < len(ids); i++ {
			net.AddLink(ids[rng.Intn(i)], ids[i], 1+rng.Intn(5))
		}
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				if rng.Float64() < 0.25 {
					func() {
						defer func() { recover() }() // skip duplicate links
						net.AddLink(ids[i], ids[j], 1+rng.Intn(5))
					}()
				}
			}
		}
		sim.Run(500)

		// Random link failures and recoveries.
		links := net.Links()
		for _, l := range links {
			if rng.Float64() < 0.3 {
				net.SetLink(l.A, l.B, false)
			}
		}
		sim.Run(sim.Now() + 500)
		assertConverged(t, net, "after failures")

		for _, l := range links {
			net.SetLink(l.A, l.B, true)
		}
		sim.Run(sim.Now() + 500)
		assertConverged(t, net, "after recovery")
	}
}

func TestOutOfOrderDeliveryAcrossSeeds(t *testing.T) {
	// Different seeds => different random message delays => different
	// reorderings. Every run must still converge to the reference.
	for seed := int64(0); seed < 20; seed++ {
		sim, net := sampleNet(seed)
		sim.Run(400)
		assertConverged(t, net, "seeded reordering")
	}
}
