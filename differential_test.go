package netsec

import (
	"math/rand"
	"testing"
)

// TestRandomDifferential runs the symbolic fixpoint and exhaustive concrete
// enumeration on many random small graphs. Because the symbolic space here is
// the tiny value range [0, universe), the domain equals the full space and the
// cross-check becomes an exact point-level equality proof.
func TestRandomDifferential(t *testing.T) {
	const universe = 12
	const trials = 1000
	rng := rand.New(rand.NewSource(20261111))
	for trial := 0; trial < trials; trial++ {
		g, entry, _, ingress := randomGraph(t, rng, universe)
		domain, err := BuildNarrowedDomain(g, ingress, nil, DomainConfig{MaxValues: universe * 4})
		if err != nil {
			t.Fatalf("trial %d: domain: %v", trial, err)
		}
		engine := newFixpointEngine(g, 0)
		if err := engine.run(entry, rectFromConstraints(ingress)); err != nil {
			t.Fatalf("trial %d: fixpoint: %v", trial, err)
		}
		enum, err := Enumerate(g, entry, ingress, domain)
		if err != nil {
			t.Fatalf("trial %d: enumerate: %v", trial, err)
		}
		for id := range g.Nodes {
			concrete := map[uint64]bool{}
			for _, cr := range enum.Reachable[id] {
				concrete[packetKey(cr.Packet)] = true
			}
			symbolic := map[uint64]bool{}
			for _, r := range engine.reachableAt(id) {
				for _, p := range domain.packetsIn(r) {
					symbolic[packetKey(p)] = true
				}
			}
			if len(concrete) != len(symbolic) {
				t.Fatalf("trial %d node %d: concrete=%d symbolic=%d", trial, id, len(concrete), len(symbolic))
			}
			for key := range concrete {
				if !symbolic[key] {
					t.Fatalf("trial %d node %d: concrete-only packet key %d", trial, id, key)
				}
			}
			for key := range symbolic {
				if !concrete[key] {
					t.Fatalf("trial %d node %d: symbolic-only packet key %d", trial, id, key)
				}
			}
		}
	}
}

func randomGraph(t *testing.T, rng *rand.Rand, universe uint32) (*Graph, int, int, []Constraint) {
	t.Helper()
	nodeCount := 2 + rng.Intn(4)
	nodes := make([]Node, nodeCount)
	for i := range nodes {
		nodes[i] = Node{ID: i + 1}
		filterCount := rng.Intn(3)
		for j := 0; j < filterCount; j++ {
			lo := rng.Uint32() % universe
			hi := lo + rng.Uint32()%uint32(universe)
			if hi >= universe {
				hi = universe - 1
			}
			nodes[i].Filters = append(nodes[i].Filters, FilterRule{
				Name:  "f",
				Field: Field(rng.Intn(2)),
				Match: Interval{Lo: lo, Hi: hi},
				Allow: rng.Intn(2) == 0,
			})
		}
		ruleCount := rng.Intn(3)
		for j := 0; j < ruleCount; j++ {
			lo := rng.Uint32() % universe
			hi := lo + rng.Uint32()%uint32(universe)
			if hi >= universe {
				hi = universe - 1
			}
			delta := int64(rng.Intn(7)) - 3
			rule := TranslateRule{
				Name:   "t",
				Target: Field(rng.Intn(2)),
				Match:  Interval{Lo: lo, Hi: hi},
				Delta:  delta,
			}
			if rng.Intn(2) == 0 {
				gLo := rng.Uint32() % universe
				gHi := gLo + rng.Uint32()%uint32(universe)
				if gHi >= universe {
					gHi = universe - 1
				}
				rule.GuardActive = true
				rule.GuardField = Field(rng.Intn(2))
				rule.Guard = Interval{Lo: gLo, Hi: gHi}
			}
			nodes[i].Translates = append(nodes[i].Translates, rule)
		}
	}
	var edges []Edge
	for i := 0; i < nodeCount*2; i++ {
		src := 1 + rng.Intn(nodeCount)
		dst := 1 + rng.Intn(nodeCount)
		edges = append(edges, Edge{Src: src, Dst: dst})
	}
	g, err := NewGraph(nodes, edges)
	if err != nil {
		t.Fatal(err)
	}
	entry := 1
	exit := nodeCount
	ingress := []Constraint{
		{Field: Src, Range: Interval{Lo: 0, Hi: universe - 1}},
		{Field: Dst, Range: Interval{Lo: 0, Hi: universe - 1}},
	}
	return g, entry, exit, ingress
}
