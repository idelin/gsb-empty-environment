package mvr

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func mustValues(t *testing.T, n *Node, want []string) {
	t.Helper()
	if got := n.Reg.Values(); !reflect.DeepEqual(got, want) {
		t.Fatalf("node %s values = %v, want %v", n.ID, got, want)
	}
}

func TestResolvePropagatesAcrossReplicas(t *testing.T) {
	s := NewSim(1)
	a := s.AddNode("A", 0)
	b := s.AddNode("B", 0)
	c := s.AddNode("C", 3600) // wall clock one hour fast
	a.Write("apple")
	b.Write("banana")
	c.Write("cherry")
	s.Run()
	for _, n := range s.Nodes() {
		mustValues(t, n, []string{"apple", "banana", "cherry"})
	}
	b.Resolve("fruit-salad")
	s.Run()
	if !s.Converged() {
		t.Fatal("replicas did not converge after resolve propagation")
	}
	for _, n := range s.Nodes() {
		mustValues(t, n, []string{"fruit-salad"})
	}
}

func TestPartitionThenHealConvergesInOneSyncRound(t *testing.T) {
	s := NewSim(7)
	a := s.AddNode("A", 0)
	b := s.AddNode("B", 0)
	c := s.AddNode("C", 0)
	s.Partition("A", "C")
	s.Partition("B", "C")
	a.Write("from-AB-side")
	c.Write("from-C-side")
	s.Run()
	// During the partition each side sees only its own write...
	mustValues(t, a, []string{"from-AB-side"})
	mustValues(t, b, []string{"from-AB-side"})
	mustValues(t, c, []string{"from-C-side"})
	// ...yet both writes survive, and one anti-entropy round after healing
	// is enough for every replica to converge (liveness bound).
	s.HealAll()
	s.SyncAll()
	s.Run()
	if !s.Converged() {
		t.Fatal("not converged after heal + one sync round")
	}
	for _, n := range s.Nodes() {
		mustValues(t, n, []string{"from-AB-side", "from-C-side"})
	}
}

func TestCrashRecoverConverges(t *testing.T) {
	s := NewSim(3)
	a := s.AddNode("A", 0)
	s.AddNode("B", 0)
	c := s.AddNode("C", 0)
	c.Crash()
	a.Write("while-C-down")
	s.Run()
	mustValues(t, c, nil) // C missed the write
	c.Recover()
	s.SyncAll()
	s.Run()
	if !s.Converged() {
		t.Fatal("not converged after recovery")
	}
	mustValues(t, c, []string{"while-C-down"})
}

func TestAvailabilityDuringPartition(t *testing.T) {
	s := NewSim(11)
	a := s.AddNode("A", 0)
	s.AddNode("B", 0)
	s.Partition("A", "B")
	// Reads and writes never block on the network.
	a.Write("local-write")
	mustValues(t, a, []string{"local-write"})
}

// TestRandomSafetyAndConvergence is the safety evidence: under random
// writes, resolves, partitions, heals, crashes, recoveries and syncs, once
// the network heals and one anti-entropy round completes, every replica
// holds exactly the set of writes that no other write causally dominates.
// No concurrent value is ever lost and no dominated value ever resurfaces.
func TestRandomSafetyAndConvergence(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			s := NewSim(seed)
			rng := rand.New(rand.NewSource(seed))
			ids := []string{"A", "B", "C", "D"}
			for _, id := range ids {
				s.AddNode(id, rng.Int63n(2000)-1000) // random clock skew
			}
			var all []Entry // oracle: every write/resolve ever issued
			counter := 0
			upNodes := func() []*Node {
				var out []*Node
				for _, n := range s.Nodes() {
					if n.Up() {
						out = append(out, n)
					}
				}
				return out
			}
			for step := 0; step < 200; step++ {
				switch rng.Intn(10) {
				case 0, 1, 2, 3: // write
					ns := upNodes()
					if len(ns) == 0 {
						continue
					}
					n := ns[rng.Intn(len(ns))]
					all = append(all, n.Write(fmt.Sprintf("v%d", counter)))
					counter++
				case 4: // explicit resolve
					ns := upNodes()
					if len(ns) == 0 {
						continue
					}
					n := ns[rng.Intn(len(ns))]
					all = append(all, n.Resolve(fmt.Sprintf("r%d", counter)))
					counter++
				case 5: // partition a random pair
					s.Partition(ids[rng.Intn(len(ids))], ids[rng.Intn(len(ids))])
				case 6: // heal
					if rng.Intn(2) == 0 {
						s.HealAll()
					} else {
						s.Heal(ids[rng.Intn(len(ids))], ids[rng.Intn(len(ids))])
					}
				case 7: // crash
					s.Node(ids[rng.Intn(len(ids))]).Crash()
				case 8: // recover
					s.Node(ids[rng.Intn(len(ids))]).Recover()
				case 9: // one anti-entropy round from a random node
					s.Node(ids[rng.Intn(len(ids))]).Sync()
				}
				// Usually flush the network; sometimes leave messages in
				// flight so later writes are genuinely concurrent.
				if rng.Intn(3) != 0 {
					s.Run()
				}
			}
			// Heal everything, recover everyone, one sync round: liveness.
			s.HealAll()
			for _, n := range s.Nodes() {
				n.Recover()
			}
			s.SyncAll()
			s.Run()
			if !s.Converged() {
				t.Fatalf("seed %d: replicas did not converge after heal + one sync round", seed)
			}
			// Oracle: a write survives iff no other write dominates it.
			want := map[string]bool{}
			for i, e := range all {
				dominated := false
				for j, o := range all {
					if i != j && Compare(e.Ver, o.Ver) == RelBefore {
						dominated = true
						break
					}
				}
				if !dominated {
					want[e.Key()] = true
				}
			}
			for _, n := range s.Nodes() {
				got := map[string]bool{}
				for _, e := range n.Reg.Entries() {
					got[e.Key()] = true
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("seed %d node %s: got %d entries, oracle wants %d", seed, n.ID, len(got), len(want))
				}
			}
		})
	}
}
