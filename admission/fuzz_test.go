package admission

import (
	"math/rand"
	"testing"
)

// randomProblem builds small, structurally varied problems so the tree-DP
// solver can be cross-checked against the independent exhaustive enumerator.
func randomProblem(rng *rand.Rand, protected bool) PublishRequest {
	n := 2 + rng.Intn(3) // 2..4 batches per side
	req := PublishRequest{PrivacyThreshold: 1 + rng.Intn(3)}
	for i := 0; i < n; i++ {
		req.Batches = append(req.Batches,
			Batch{ID: "L" + itoa(i), Side: "L", Category: "cat" + itoa(rng.Intn(2))},
			Batch{ID: "R" + itoa(i), Side: "R", Category: "cat" + itoa(rng.Intn(2))},
		)
	}
	cats := []string{"A", "B", "C"}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if rng.Intn(2) == 0 {
				req.Edges = append(req.Edges, Edge{
					L: "L" + itoa(i), R: "R" + itoa(j),
					JointCategory: cats[rng.Intn(len(cats))],
				})
			}
		}
	}
	if len(req.Edges) == 0 {
		req.Edges = append(req.Edges,
			Edge{L: "L0", R: "R0", JointCategory: "A"},
			Edge{L: "L1", R: "R1", JointCategory: "B"},
		)
	}

	// Occasionally merge disjoint edges into shared-decision groups.
	usedEdges := map[int]bool{}
	for k := 0; k < len(req.Edges)-1 && rng.Intn(2) == 0; k++ {
		i := rng.Intn(len(req.Edges))
		j := rng.Intn(len(req.Edges))
		if i == j || usedEdges[i] || usedEdges[j] {
			continue
		}
		ei, ej := req.Edges[i], req.Edges[j]
		if ei.L == ej.L || ei.R == ej.R {
			continue
		}
		req.Groups = append(req.Groups, DecisionGroup{
			ID:          "g" + itoa(len(req.Groups)),
			MemberEdges: []string{edgeKey(ei), edgeKey(ej)},
		})
		usedEdges[i] = true
		usedEdges[j] = true
	}

	if rng.Intn(2) == 0 {
		req.Quota.Total = n
	}
	if rng.Intn(2) == 0 {
		req.Quota.MaxPerCategory = map[string]int{}
		for _, c := range cats {
			if rng.Intn(2) == 0 {
				req.Quota.MaxPerCategory[c] = rng.Intn(n + 1)
			}
		}
	}

	if protected {
		req.ProtectedBatches = []string{"L0", "R0"}
	}
	return req
}

func TestRandomSolverMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20261121))
	for iter := 0; iter < 300; iter++ {
		req := randomProblem(rng, true)
		p, err := build(req, DefaultOptions())
		if err != nil {
			t.Fatalf("iter %d build: %v", iter, err)
		}

		wantFeasible := bruteForce(p)
		report, err := Admit(req)
		if err != nil {
			t.Fatalf("iter %d Admit: %v", iter, err)
		}
		gotFeasible := report.Decision != DecisionInfeasible
		if wantFeasible != gotFeasible {
			t.Fatalf("iter %d feasibility mismatch: brute=%v report=%q", iter, wantFeasible, report.Decision)
		}
		if !wantFeasible {
			continue
		}

		s := newSolver(p)
		for _, id := range req.ProtectedBatches {
			wantJoint, wantCounterpart := bruteAmbiguity(p, id)
			got := s.ambiguity(id)
			if got.PossibleJointCategories != wantJoint {
				t.Fatalf("iter %d batch %s joint categories: solver=%d brute=%d",
					iter, id, got.PossibleJointCategories, wantJoint)
			}
			if got.PossibleCounterpartCategories != wantCounterpart {
				t.Fatalf("iter %d batch %s counterpart categories: solver=%d brute=%d",
					iter, id, got.PossibleCounterpartCategories, wantCounterpart)
			}
		}
	}
}

func TestRandomDecompositionMatchesWhole(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	for iter := 0; iter < 100; iter++ {
		req := randomProblem(rng, true)

		whole, err := Admit(req)
		if err != nil {
			t.Fatalf("iter %d whole: %v", iter, err)
		}

		// A random split into two edge-disjoint blocks sharing at most the
		// batches that appear on edges in both pieces.
		dec := randomTwoBlockSplit(req, rng)
		if dec == nil {
			continue
		}
		if !groupsStayInsideBlocks(req, dec) {
			continue
		}
		req.Decomposition = dec
		split, err := Admit(req)
		if err != nil {
			t.Fatalf("iter %d split rejected: %v req=%+v", iter, err, req)
		}
		if split.Decision != whole.Decision {
			t.Fatalf("iter %d decision mismatch: whole=%q split=%q req=%+v dec=%+v",
				iter, whole.Decision, split.Decision, req, dec)
		}
		for i := range split.Batches {
			if split.Batches[i].PossibleJointCategories != whole.Batches[i].PossibleJointCategories {
				t.Fatalf("iter %d category count mismatch whole=%+v split=%+v req=%+v dec=%+v",
					iter, whole.Batches, split.Batches, req, dec)
			}
		}
	}
}

func groupsStayInsideBlocks(req PublishRequest, dec *Decomposition) bool {
	owner := map[string]int{}
	for b, blk := range dec.Blocks {
		for _, key := range blk.EdgeKeys {
			owner[key] = b
		}
	}
	for _, g := range req.Groups {
		first := owner[g.MemberEdges[0]]
		for _, key := range g.MemberEdges[1:] {
			if owner[key] != first {
				return false
			}
		}
	}
	return true
}

func randomTwoBlockSplit(req PublishRequest, rng *rand.Rand) *Decomposition {
	var left, right []string
	for _, e := range req.Edges {
		key := edgeKey(e)
		if rng.Intn(2) == 0 {
			left = append(left, key)
		} else {
			right = append(right, key)
		}
	}
	if len(left) == 0 || len(right) == 0 {
		return nil
	}
	shared := map[string]int{}
	for _, keys := range [][]string{left, right} {
		batches := map[string]bool{}
		for _, key := range keys {
			e := findEdge(req.Edges, key)
			batches[e.L] = true
			batches[e.R] = true
		}
		for b := range batches {
			shared[b]++
		}
	}
	width := 0
	for _, n := range shared {
		if n == 2 {
			width++
		}
	}
	if width == 0 || width > 4 {
		return nil
	}
	return &Decomposition{Blocks: []Block{
		{ID: "b0", EdgeKeys: left},
		{ID: "b1", EdgeKeys: right},
	}}
}

func findEdge(edges []Edge, key string) Edge {
	for _, e := range edges {
		if edgeKey(e) == key {
			return e
		}
	}
	return Edge{}
}
