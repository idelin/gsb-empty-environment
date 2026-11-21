package admission

import (
	"testing"
	"time"
)

// TestThousandRows verifies a roughly thousand-row problem is handled through
// block-tree boundary state propagation rather than global pairing
// enumeration. Each block stays tiny; only separator masks and aggregate
// category vectors cross block boundaries.
func TestThousandRows(t *testing.T) {
	const blocks = 250
	req, dec := chainProblem(blocks)
	req.Decomposition = dec
	req.ProtectedBatches = []string{req.Batches[0].ID, "L-priv" + itoa(blocks-1)}

	if len(req.Batches) < 1000 {
		t.Fatalf("test setup too small: %d batches", len(req.Batches))
	}

	start := time.Now()
	report, err := Admit(req)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("thousand-row Admit: %v", err)
	}
	if report.Decision == DecisionInfeasible {
		t.Fatal("chain problem should remain feasible at scale")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("thousand-row solve took %s, suggesting global enumeration", elapsed)
	}

	p, err := build(req, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.blockEdges) != blocks {
		t.Fatalf("blocks = %d, want %d", len(p.blockEdges), blocks)
	}

	// Ambiguity queries at scale must reuse the cached boundary messages and
	// stay well inside the time budget.
	s := newSolver(p)
	s.feasible(-1, false)
	start = time.Now()
	for _, id := range req.ProtectedBatches {
		entry := s.ambiguity(id)
		if entry.PossibleJointCategories == 0 {
			t.Fatalf("protected batch %s has no possible joint category", id)
		}
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("ambiguity queries at scale were too slow")
	}

	// The local enumeration count per block must remain tiny; the global
	// number of pairings is never materialized.
	for b, info := range s.infos {
		total := 0
		for _, states := range info.local {
			total += len(states)
		}
		if total > 64 {
			t.Fatalf("block %d enumerated %d local states, expected a small constant", b, total)
		}
	}
	t.Logf("scale: %d batches, %d edges, %d blocks, decision=%s, solve=%s",
		len(req.Batches), len(req.Edges), blocks, report.Decision, elapsed)
}
