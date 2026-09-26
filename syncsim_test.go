package syncsim

import "testing"

// ---- shared test helpers ----

func addNodes(s *Sim, ids ...string) []*Node {
	var out []*Node
	for _, id := range ids {
		out = append(out, s.AddNode(id))
	}
	return out
}

func storesEqual(a, b *Store) bool {
	ra, rb := a.Records(), b.Records()
	if len(ra) != len(rb) {
		return false
	}
	for k, va := range ra {
		vb, ok := rb[k]
		if !ok || va != vb {
			return false
		}
	}
	return true
}

func allConverged(nodes []*Node) bool {
	for i := 1; i < len(nodes); i++ {
		if !storesEqual(nodes[0].Store, nodes[i].Store) {
			return false
		}
	}
	return true
}

func totalApplied(nodes []*Node) int {
	t := 0
	for _, n := range nodes {
		t += n.Stats.Applied
	}
	return t
}

func totalRecordsSent(nodes []*Node) int {
	t := 0
	for _, n := range nodes {
		t += n.Stats.RecordsSent
	}
	return t
}

// syncRound runs one all-pairs anti-entropy round and drains the network.
func syncRound(s *Sim, nodes []*Node) {
	for i := range nodes {
		for j := range nodes {
			if i != j {
				nodes[i].StartSync(nodes[j].ID)
			}
		}
	}
	s.Run()
}

// runUntilFixpoint repeats rounds until a full round applies nothing,
// i.e. repeated synchronisation has reached a fixpoint.
func runUntilFixpoint(s *Sim, nodes []*Node, maxRounds int) (rounds int, ok bool) {
	for r := 1; r <= maxRounds; r++ {
		before := totalApplied(nodes)
		syncRound(s, nodes)
		if totalApplied(nodes) == before {
			return r, allConverged(nodes)
		}
	}
	return maxRounds, false
}

func mustConverge(t *testing.T, nodes []*Node) {
	t.Helper()
	if !allConverged(nodes) {
		for _, n := range nodes {
			t.Logf("node %s: %v", n.ID, n.Store.Records())
		}
		t.Fatal("nodes diverged")
	}
}
