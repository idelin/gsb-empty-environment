package admission

import (
	"strings"
	"testing"
)

// chainProblem builds a balanced problem of n blocks connected in a chain
// through one shared L and one shared R batch between adjacent blocks. Each
// block is a small 2x2 grid, so local enumeration stays tiny while the global
// problem never gets enumerated.
// chainProblem builds a balanced problem of nBlocks blocks connected in a
// chain through single shared L separator batches (separator width one, well
// within the limit of four).
//
// Block b owns two private R batches. Its L batches are:
//   - a private L batch with a self-contained, always force-includable edge;
//   - a left separator L (except block 0, which gets a private terminal L);
//   - a right separator L (except the last block, which gets one too).
//
// Adjacent blocks share exactly one L batch, no edge is ever duplicated, and
// every chain length admits a perfect matching, while each block enumerates
// only a constant number of local states.
func chainProblem(nBlocks int) (PublishRequest, *Decomposition) {
	var batches []Batch
	var edges []Edge
	var blocks []Block
	batchSeen := map[string]bool{}
	addBatch := func(id, side, cat string) {
		if batchSeen[id] {
			return
		}
		batchSeen[id] = true
		batches = append(batches, Batch{ID: id, Side: side, Category: cat})
	}

	for b := 0; b < nBlocks; b++ {
		// Balanced local component with a private perfect-matching pair and
		// symmetric left/right separators, each carrying one L and one R
		// (separator width two, within the limit of four).
		lPriv := "L-priv" + itoa(b)
		rPriv := "R-priv" + itoa(b)
		addBatch(lPriv, "L", "x")
		addBatch(rPriv, "R", "y")

		// Left separator (or private terminals for the first block).
		lLeft := "L-termL" + itoa(b)
		rLeft := "R-termL" + itoa(b)
		if b > 0 {
			lLeft = "L-link" + itoa(b-1)
			rLeft = "R-link" + itoa(b-1)
		}
		addBatch(lLeft, "L", "x")
		addBatch(rLeft, "R", "y")

		// Right separator (or private terminals for the last block).
		lRight := "L-termR" + itoa(b)
		rRight := "R-termR" + itoa(b)
		if b < nBlocks-1 {
			lRight = "L-link" + itoa(b)
			rRight = "R-link" + itoa(b)
		}
		addBatch(lRight, "L", "x")
		addBatch(rRight, "R", "y")

		grid := []Edge{
			// Force-includable self-contained private edge.
			{L: lPriv, R: rPriv, JointCategory: "J"},
		}
		// The left separator edge is owned only by the first block; for an
		// interior block the left separator batches are shared with the
		// preceding block but referenced through this block's own edges below.
		if b == 0 {
			grid = append(grid, Edge{L: lLeft, R: rLeft, JointCategory: "J"})
		} else {
			// Touch the left separator batches through this block's private
			// batches so they are recognized as shared, without duplicating
			// the preceding block's separator edge.
			grid = append(grid,
				Edge{L: lPriv, R: rLeft, JointCategory: "J"},
				Edge{L: lLeft, R: rPriv, JointCategory: "J"},
			)
		}
		// The right separator edge belongs to this block; the next block will
		// reuse the same separator batches via different, non-duplicate edges.
		grid = append(grid, Edge{L: lRight, R: rRight, JointCategory: "J"})
		var keys []string
		for _, e := range grid {
			edges = append(edges, e)
			keys = append(keys, edgeKey(e))
		}
		blocks = append(blocks, Block{ID: "blk-" + itoa(b), EdgeKeys: keys})
	}

	req := PublishRequest{
		Batches:          batches,
		Edges:            edges,
		PrivacyThreshold: 2,
	}
	return req, &Decomposition{Blocks: blocks}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func TestDecompositionAcceptedAndMatchesBruteForce(t *testing.T) {
	req, dec := chainProblem(3)
	req.Decomposition = dec
	report, err := Admit(req)
	if err != nil {
		t.Fatalf("legal decomposition rejected: %v", err)
	}
	if report.Decision == DecisionInfeasible {
		t.Fatal("chain problem should be feasible")
	}

	p, err := build(req, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.blockEdges) != 3 {
		t.Fatalf("blocks = %d, want 3", len(p.blockEdges))
	}
	for _, sep := range flattenSeparators(p) {
		if len(sep) > 4 {
			t.Fatalf("separator width %d exceeds four", len(sep))
		}
	}
	if !bruteForce(p) {
		t.Fatal("brute force disagrees on chain feasibility")
	}
}

func flattenSeparators(p *problem) [][]string {
	var out [][]string
	for _, neigh := range p.blockTree {
		for _, sep := range neigh {
			out = append(out, sep)
		}
	}
	return out
}

func TestIllegalDecompositionsRejected(t *testing.T) {
	base, good := chainProblem(2)

	cases := map[string]func(*Decomposition){
		"edge missing": func(d *Decomposition) {
			d.Blocks[1].EdgeKeys = d.Blocks[1].EdgeKeys[:1]
		},
		"edge duplicated across blocks": func(d *Decomposition) {
			d.Blocks[1].EdgeKeys = append(d.Blocks[1].EdgeKeys, d.Blocks[0].EdgeKeys[0])
		},
		"disconnected blocks": func(d *Decomposition) {
			// An isolated extra block that owns a fresh edge but shares no
			// batch with the existing chain makes the block graph
			// disconnected.
			d.Blocks = append(d.Blocks, Block{ID: "extra", EdgeKeys: []string{"L-isolated|R-isolated"}})
		},
		"unknown edge": func(d *Decomposition) {
			d.Blocks[0].EdgeKeys[0] = "Nope|Nothing"
		},
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			req := base
			req.Batches = append(append([]Batch(nil), base.Batches...),
				Batch{ID: "L-isolated", Side: "L", Category: "x"},
				Batch{ID: "R-isolated", Side: "R", Category: "y"},
			)
			req.Edges = append(append([]Edge(nil), base.Edges...),
				Edge{L: "L-isolated", R: "R-isolated", JointCategory: "J"},
			)
			d := cloneDecomposition(good)
			mutate(d)
			req.Decomposition = d
			if _, err := Admit(req); err == nil {
				t.Fatalf("%s: expected rejection error", name)
			}
		})
	}
}

func TestGroupSpanningBlocksRejected(t *testing.T) {
	req, dec := chainProblem(2)
	req.Decomposition = dec
	req.Groups = []DecisionGroup{
		{ID: "span", MemberEdges: []string{
			dec.Blocks[0].EdgeKeys[0],
			dec.Blocks[1].EdgeKeys[0],
		}},
	}
	_, err := Admit(req)
	if err == nil || !strings.Contains(err.Error(), "spans multiple blocks") {
		t.Fatalf("got %v, want group-spanning-block error", err)
	}
}

// TestWideSeparatorRejected builds two blocks sharing five batches and checks
// the separator-width limit of four is enforced.
func TestWideSeparatorRejected(t *testing.T) {
	var batches []Batch
	var edges []Edge
	var block0, block1 []string
	for i := 0; i < 5; i++ {
		l, r := "L"+itoa(i), "R"+itoa(i)
		batches = append(batches,
			Batch{ID: l, Side: "L", Category: "x"},
			Batch{ID: r, Side: "R", Category: "y"},
		)
	}
	// Five shared L batches; each block gives them distinct edges to its own
	// private R batches, so edges are unique while all five L batches are
	// shared across the two blocks.
	for i := 0; i < 5; i++ {
		l := "L" + itoa(i)
		rA, rB := "RA"+itoa(i), "RB"+itoa(i)
		batches = append(batches,
			Batch{ID: rA, Side: "R", Category: "y"},
			Batch{ID: rB, Side: "R", Category: "y"},
		)
		eA := Edge{L: l, R: rA, JointCategory: "J"}
		eB := Edge{L: l, R: rB, JointCategory: "J"}
		edges = append(edges, eA, eB)
		block0 = append(block0, edgeKey(eA))
		block1 = append(block1, edgeKey(eB))
	}
	req := PublishRequest{
		Batches:          batches,
		Edges:            edges,
		PrivacyThreshold: 2,
		Decomposition: &Decomposition{Blocks: []Block{
			{ID: "b0", EdgeKeys: block0},
			{ID: "b1", EdgeKeys: block1},
		}},
	}
	_, err := Admit(req)
	if err == nil || !strings.Contains(err.Error(), "at most four") {
		t.Fatalf("got %v, want separator-width error", err)
	}
}

func cloneDecomposition(d *Decomposition) *Decomposition {
	out := &Decomposition{Blocks: make([]Block, len(d.Blocks))}
	for i, b := range d.Blocks {
		out.Blocks[i] = Block{ID: b.ID, EdgeKeys: append([]string(nil), b.EdgeKeys...)}
	}
	return out
}

func TestBoundaryStateReusedAcrossQueries(t *testing.T) {
	req, dec := chainProblem(3)
	req.Decomposition = dec
	req.ProtectedBatches = []string{req.Batches[0].ID}
	p, err := build(req, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	s := newSolver(p)

	// Warm the cache with an unconstrained run.
	s.feasible(-1, false)
	if !s.cacheOK[p.blockRoot] {
		t.Fatal("root boundary message was not cached")
	}
	baseline := append([]int(nil), s.recomputes...)

	// A forced-group query in block 1 recomputes only the root-to-block
	// path; every off-path block must keep its cached message. Pick a group
	// whose forced inclusion is actually part of some feasible pairing.
	targetGroup := feasibleGroupInBlock(s, p, 1)
	if targetGroup < 0 {
		t.Fatal("no force-includable group found in block 1")
	}
	if !s.feasible(targetGroup, true) {
		t.Fatal("forced include query unexpectedly infeasible")
	}
	forcedBlock := p.blockOfGroup[targetGroup]
	path := s.ancestorPathSet(forcedBlock)
	for b := range p.blockEdges {
		if !path[b] && s.recomputes[b] != baseline[b] {
			t.Fatalf("off-path block %d boundary state was rebuilt instead of reused", b)
		}
	}

	// A second forced query in the deepest block likewise reuses off-path
	// boundary messages, including the previously touched subtree.
	leafGroup := feasibleGroupInBlock(s, p, len(p.blockEdges)-1)
	if leafGroup < 0 {
		t.Fatal("no force-includable group found in leaf block")
	}
	if !s.feasible(leafGroup, true) {
		t.Fatal("leaf forced query unexpectedly infeasible")
	}
	leafBlock := p.blockOfGroup[leafGroup]
	leafPath := s.ancestorPathSet(leafBlock)
	for b := range p.blockEdges {
		if !leafPath[b] && s.recomputes[b] != baseline[b] {
			t.Fatalf("off-path block %d boundary state was rebuilt instead of reused", b)
		}
	}
}

func firstGroupInBlock(p *problem, block int) int {
	for ei, bi := range p.blockOfEdge {
		if bi == block {
			return p.edgeGroup[ei]
		}
	}
	return -1
}

func feasibleGroupInBlock(s *solver, p *problem, block int) int {
	for ei, bi := range p.blockOfEdge {
		if bi != block {
			continue
		}
		gi := p.edgeGroup[ei]
		if s.feasible(gi, true) {
			return gi
		}
	}
	return -1
}
