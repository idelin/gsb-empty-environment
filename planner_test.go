package recovery

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"strings"
	"testing"
)

func TestSharedBlockTieBreak(t *testing.T) {
	in := Input{
		LogicalVolumes: []LogicalVolume{
			{Candidates: []Candidate{
				{PhysicalVolume: "pv0", Node: "left"},
				{PhysicalVolume: "pv1", Node: "plain"},
			}},
			{Candidates: []Candidate{
				{PhysicalVolume: "pv2", Node: "right"},
				{PhysicalVolume: "pv3", Node: "plain"},
			}},
		},
		PhysicalVolumes: []PhysicalVolume{
			{ID: "pv0", Nodes: []TreeNode{
				{ID: "r"},
				{ID: "left", Parent: "r", Blocks: []BlockID{"shared", "a"}},
			}},
			{ID: "pv1", Nodes: []TreeNode{{ID: "r"}, {ID: "plain", Parent: "r"}}},
			{ID: "pv2", Nodes: []TreeNode{
				{ID: "r"},
				{ID: "right", Parent: "r", Blocks: []BlockID{"shared", "b"}},
			}},
			{ID: "pv3", Nodes: []TreeNode{
				{ID: "r"},
				{ID: "plain", Parent: "r", Blocks: []BlockID{"shared"}},
			}},
		},
		BlockCosts: map[BlockID]*big.Int{
			"shared": big.NewInt(7),
			"a":      big.NewInt(2),
			"b":      big.NewInt(3),
		},
	}
	res, err := PlanBest(in)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Feasible || res.Count.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("feasible=%v count=%v", res.Feasible, res.Count)
	}
	if res.DownloadCost.Cmp(big.NewInt(7)) != 0 {
		t.Fatalf("shared block was not charged once: cost=%v", res.DownloadCost)
	}
	if got := res.Certificate.Choices[0].Candidate; got != 1 {
		t.Fatalf("volume 0 candidate=%d, want 1", got)
	}
	if got := res.Certificate.Choices[1].Candidate; got != 1 {
		t.Fatalf("volume 1 candidate=%d, want 1", got)
	}
}

func TestSharedBlockAcrossDistantBranches(t *testing.T) {
	in := Input{
		LogicalVolumes: []LogicalVolume{
			{Candidates: []Candidate{{PhysicalVolume: "pv0", Node: "r"}}},
			{Candidates: []Candidate{{PhysicalVolume: "pv1", Node: "r"}}},
			{Candidates: []Candidate{{PhysicalVolume: "pv2", Node: "r"}}},
		},
		PhysicalVolumes: []PhysicalVolume{
			{ID: "pv0", Nodes: []TreeNode{{ID: "r", Blocks: []BlockID{"shared"}}}},
			{ID: "pv1", Nodes: []TreeNode{{ID: "r"}}},
			{ID: "pv2", Nodes: []TreeNode{{ID: "r", Blocks: []BlockID{"shared"}}}},
		},
		BlockCosts: map[BlockID]*big.Int{"shared": big.NewInt(5)},
	}
	res, err := PlanBest(in)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Feasible {
		t.Fatal("expected feasible")
	}
	if res.DownloadCost.Cmp(big.NewInt(5)) != 0 {
		t.Fatalf("distant shared block cost=%v, want 5", res.DownloadCost)
	}
}

func TestBenefitDominatesCost(t *testing.T) {
	in := Input{
		LogicalVolumes: []LogicalVolume{{Candidates: []Candidate{
			{PhysicalVolume: "A", Node: "r"},
			{PhysicalVolume: "B", Node: "r"},
		}}},
		PhysicalVolumes: []PhysicalVolume{
			{ID: "A", Nodes: []TreeNode{{ID: "r", Marks: []Mark{{Txn: 1, Volume: 0}}, Blocks: []BlockID{"expensive"}}}},
			{ID: "B", Nodes: []TreeNode{{ID: "r"}}},
		},
		Transactions: []Transaction{{ID: 1, Participants: []int{0}, Benefit: big.NewInt(1)}},
		BlockCosts:   map[BlockID]*big.Int{"expensive": big.NewInt(100)},
	}
	res, err := PlanBest(in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Benefit.Cmp(big.NewInt(1)) != 0 || res.DownloadCost.Cmp(big.NewInt(100)) != 0 {
		t.Fatalf("got benefit=%v cost=%v, want benefit 1 before cost 100", res.Benefit, res.DownloadCost)
	}
	if res.Certificate.Choices[0].Candidate != 0 {
		t.Fatalf("cost tie-break overrode benefit: chose %d", res.Certificate.Choices[0].Candidate)
	}
}

func TestPhysicalVolumeCannotBeUsedTwice(t *testing.T) {
	in := Input{
		LogicalVolumes: []LogicalVolume{
			{Candidates: []Candidate{
				{PhysicalVolume: "A", Node: "r"},
				{PhysicalVolume: "B", Node: "r"},
			}},
			{Candidates: []Candidate{
				{PhysicalVolume: "A", Node: "r"},
				{PhysicalVolume: "C", Node: "r"},
			}},
		},
		PhysicalVolumes: []PhysicalVolume{
			{ID: "A", Nodes: []TreeNode{{ID: "r"}}},
			{ID: "B", Nodes: []TreeNode{{ID: "r"}}},
			{ID: "C", Nodes: []TreeNode{{ID: "r"}}},
		},
	}
	res, err := PlanBest(in)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Feasible || res.Count.Cmp(big.NewInt(3)) != 0 {
		t.Fatalf("feasible=%v count=%v, want 3 legal assignments", res.Feasible, res.Count)
	}
	if res.Certificate.Choices[0].Candidate != 0 || res.Certificate.Choices[1].Candidate != 1 {
		t.Fatalf("rank 1 skipped the conflicting assignment incorrectly: %v %v",
			res.Certificate.Choices[0].Candidate, res.Certificate.Choices[1].Candidate)
	}
}

func TestDuplicateAndMissingMarks(t *testing.T) {
	base := func(required []TxnID) Input {
		return Input{
			LogicalVolumes: []LogicalVolume{
				{Candidates: []Candidate{{PhysicalVolume: "pv0", Node: "r"}}},
				{Candidates: []Candidate{{PhysicalVolume: "pv1", Node: "r"}}},
			},
			PhysicalVolumes: []PhysicalVolume{
				{ID: "pv0", Nodes: []TreeNode{{ID: "r", Marks: []Mark{
					{Txn: 9, Volume: 0}, {Txn: 9, Volume: 0},
				}}}},
				{ID: "pv1", Nodes: []TreeNode{{ID: "r"}}},
			},
			Transactions: []Transaction{{
				ID: 9, Participants: []int{0, 1}, Benefit: big.NewInt(5),
			}},
			Required: required,
		}
	}

	res, err := PlanBest(base([]TxnID{9}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Feasible {
		t.Fatalf("required transaction with duplicate+missing marks must be infeasible")
	}

	res, err = PlanBest(base(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Feasible || res.Benefit.Sign() != 0 {
		t.Fatalf("optional malformed transaction must be feasible with zero benefit: %+v", res)
	}
	if len(res.Certificate.Committed) != 0 {
		t.Fatalf("malformed transaction counted as committed: %v", res.Certificate.Committed)
	}
}

func TestNegativePrerequisiteForced(t *testing.T) {
	in := Input{
		LogicalVolumes: []LogicalVolume{
			{Candidates: []Candidate{
				{PhysicalVolume: "pv00", Node: "r"},
				{PhysicalVolume: "pv01", Node: "r"},
			}},
			{Candidates: []Candidate{
				{PhysicalVolume: "pv10", Node: "r"},
				{PhysicalVolume: "pv11", Node: "r"},
			}},
		},
		PhysicalVolumes: []PhysicalVolume{
			{ID: "pv00", Nodes: []TreeNode{{ID: "r", Marks: []Mark{
				{Txn: 1, Volume: 0}, {Txn: 2, Volume: 0},
			}}}},
			{ID: "pv01", Nodes: []TreeNode{{ID: "r"}}},
			{ID: "pv10", Nodes: []TreeNode{{ID: "r", Marks: []Mark{{Txn: 2, Volume: 1}}}}},
			{ID: "pv11", Nodes: []TreeNode{{ID: "r", Marks: []Mark{
				{Txn: 2, Volume: 1}, {Txn: 1, Volume: 1},
			}}}},
		},
		Transactions: []Transaction{
			{ID: 1, Participants: []int{0, 1}, Benefit: big.NewInt(-4)},
			{ID: 2, Participants: []int{0, 1}, Benefit: big.NewInt(10), Prerequisites: []TxnID{1}},
		},
	}
	res, err := PlanBest(in)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Feasible {
		t.Fatal("expected a feasible plan")
	}
	if res.Benefit.Cmp(big.NewInt(6)) != 0 {
		t.Fatalf("benefit=%v, want 6 (negative predecessor + successor)", res.Benefit)
	}
	if res.Count.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("count=%v, want 1", res.Count)
	}
	if res.Certificate.Choices[0].Candidate != 0 || res.Certificate.Choices[1].Candidate != 1 {
		t.Fatalf("unexpected choices: %v %v",
			res.Certificate.Choices[0].Candidate, res.Certificate.Choices[1].Candidate)
	}
	if len(res.Certificate.Committed) != 2 {
		t.Fatalf("committed=%v, want both transactions", res.Certificate.Committed)
	}
}

func TestPrerequisiteClosingLaterIsForced(t *testing.T) {
	in := Input{
		LogicalVolumes: []LogicalVolume{
			{Candidates: []Candidate{
				{PhysicalVolume: "pv00", Node: "r"},
				{PhysicalVolume: "pv01", Node: "r"},
			}},
			{Candidates: []Candidate{
				{PhysicalVolume: "pv10", Node: "r"},
				{PhysicalVolume: "pv11", Node: "r"},
			}},
		},
		PhysicalVolumes: []PhysicalVolume{
			{ID: "pv00", Nodes: []TreeNode{{ID: "r", Marks: []Mark{{Txn: 2, Volume: 0}}}}},
			{ID: "pv01", Nodes: []TreeNode{{ID: "r"}}},
			{ID: "pv10", Nodes: []TreeNode{{ID: "r"}}},
			{ID: "pv11", Nodes: []TreeNode{{ID: "r", Marks: []Mark{{Txn: 1, Volume: 1}}}}},
		},
		Transactions: []Transaction{
			{ID: 1, Participants: []int{1}, Benefit: big.NewInt(-4)},
			{ID: 2, Participants: []int{0}, Benefit: big.NewInt(10), Prerequisites: []TxnID{1}},
		},
	}
	res, err := PlanBest(in)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Feasible || res.Benefit.Cmp(big.NewInt(6)) != 0 {
		t.Fatalf("feasible=%v benefit=%v, want 6", res.Feasible, res.Benefit)
	}
	if res.Certificate.Choices[0].Candidate != 0 || res.Certificate.Choices[1].Candidate != 1 {
		t.Fatalf("late prerequisite not retained: choices %v %v",
			res.Certificate.Choices[0].Candidate, res.Certificate.Choices[1].Candidate)
	}
}

func TestGenerationVerificationIsAllOrNothing(t *testing.T) {
	in := Input{
		LogicalVolumes: []LogicalVolume{
			{Candidates: []Candidate{{PhysicalVolume: "A", Node: "r"}}},
			{Candidates: []Candidate{{PhysicalVolume: "B", Node: "r"}}},
		},
		PhysicalVolumes: []PhysicalVolume{
			{ID: "A", Generation: 5, Nodes: []TreeNode{{
				ID: "r", Generation: 11, Digest: "digestA",
			}}},
			{ID: "B", Generation: 7, Nodes: []TreeNode{{
				ID: "r", Generation: 13, Digest: "digestB",
			}}},
		},
	}
	res, err := PlanBest(in)
	if err != nil {
		t.Fatal(err)
	}
	good := map[PhysicalVolumeID]Observation{
		"A": {PhysicalVolume: "A", VolumeGeneration: 5, Node: "r", SnapshotGeneration: 11, SnapshotDigest: "digestA"},
		"B": {PhysicalVolume: "B", VolumeGeneration: 7, Node: "r", SnapshotGeneration: 13, SnapshotDigest: "digestB"},
	}
	choices, err := res.Certificate.Confirm(good)
	if err != nil {
		t.Fatalf("matching observations rejected: %v", err)
	}
	if len(choices) != 2 {
		t.Fatalf("confirmed %d choices, want 2", len(choices))
	}

	bad := map[PhysicalVolumeID]Observation{
		"A": {PhysicalVolume: "A", VolumeGeneration: 5, Node: "r", SnapshotGeneration: 11, SnapshotDigest: "digestA"},
		"B": {PhysicalVolume: "B", VolumeGeneration: 8, Node: "r", SnapshotGeneration: 13, SnapshotDigest: "digestB"},
	}
	choices, err = res.Certificate.Confirm(bad)
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("err=%v, want ErrVerificationFailed", err)
	}
	if choices != nil {
		t.Fatalf("partial mount group returned after verification failure: %v", choices)
	}
	var ve *VerificationError
	if !errors.As(err, &ve) || len(ve.Mismatches) == 0 {
		t.Fatalf("expected typed verification mismatch, got %T %v", err, err)
	}
	if !strings.Contains(err.Error(), "generation") {
		t.Fatalf("verification error does not describe the mismatch: %v", err)
	}
}

func TestExactLargeIntegers(t *testing.T) {
	benefit := new(big.Int).Lsh(big.NewInt(1), 100)
	benefit.Add(benefit, big.NewInt(7))
	cost0 := new(big.Int).Lsh(big.NewInt(1), 90)
	cost0.Add(cost0, big.NewInt(3))
	cost1 := new(big.Int).Lsh(big.NewInt(1), 80)
	wantCost := new(big.Int).Add(cost0, cost1)

	in := Input{
		LogicalVolumes: []LogicalVolume{
			{Candidates: []Candidate{{PhysicalVolume: "A", Node: "r"}}},
			{Candidates: []Candidate{{PhysicalVolume: "B", Node: "r"}}},
		},
		PhysicalVolumes: []PhysicalVolume{
			{ID: "A", Nodes: []TreeNode{{ID: "r", Marks: []Mark{{Txn: 1, Volume: 0}}, Blocks: []BlockID{"a"}}}},
			{ID: "B", Nodes: []TreeNode{{ID: "r", Marks: []Mark{{Txn: 1, Volume: 1}}, Blocks: []BlockID{"b"}}}},
		},
		Transactions: []Transaction{{
			ID: 1, Participants: []int{0, 1}, Benefit: benefit,
		}},
		BlockCosts: map[BlockID]*big.Int{"a": cost0, "b": cost1},
	}
	res, err := PlanBest(in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Benefit.Cmp(benefit) != 0 {
		t.Fatalf("benefit=%v, want %v", res.Benefit, benefit)
	}
	if res.DownloadCost.Cmp(wantCost) != 0 {
		t.Fatalf("cost=%v, want %v", res.DownloadCost, wantCost)
	}
}

func TestRankOutOfRange(t *testing.T) {
	in := Input{
		LogicalVolumes:  []LogicalVolume{{Candidates: []Candidate{{PhysicalVolume: "A", Node: "r"}}}},
		PhysicalVolumes: []PhysicalVolume{{ID: "A", Nodes: []TreeNode{{ID: "r"}}}},
	}
	if _, err := PlanRank(in, 0); !errors.Is(err, ErrRankOutOfRange) {
		t.Fatalf("rank 0 err=%v", err)
	}
	if _, err := PlanRank(in, 2); !errors.Is(err, ErrRankOutOfRange) {
		t.Fatalf("rank 2 err=%v", err)
	}
	if _, err := PlanRank(in, 1); err != nil {
		t.Fatalf("rank 1 err=%v", err)
	}
}

func TestUnsupportedSizes(t *testing.T) {
	var many Input
	for v := 0; v < MaxLogicalVolumes+1; v++ {
		many.LogicalVolumes = append(many.LogicalVolumes, LogicalVolume{})
	}
	if _, err := PlanBest(many); !errors.Is(err, ErrUnsupportedSize) {
		t.Fatalf("too many volumes err=%v", err)
	}

	var wide Input
	lv := LogicalVolume{}
	for c := 0; c < MaxCandidatesPerVolume+1; c++ {
		lv.Candidates = append(lv.Candidates, Candidate{})
	}
	wide.LogicalVolumes = []LogicalVolume{lv}
	if _, err := PlanBest(wide); !errors.Is(err, ErrUnsupportedSize) {
		t.Fatalf("too many candidates err=%v", err)
	}
}

func TestInvalidStructures(t *testing.T) {
	badTrees := []PhysicalVolume{
		{ID: "A", Nodes: []TreeNode{{ID: "r"}, {ID: "r"}}},
		{ID: "A", Nodes: []TreeNode{{ID: "r", Parent: "x"}}},
		{ID: "A", Nodes: []TreeNode{{ID: "r"}, {ID: "x", Parent: "x"}}},
		{ID: "A", Nodes: []TreeNode{{ID: "r"}, {ID: "x", Parent: "y"}, {ID: "y", Parent: "x"}}},
	}
	for i, pv := range badTrees {
		in := Input{
			LogicalVolumes:  []LogicalVolume{{Candidates: []Candidate{{PhysicalVolume: pv.ID, Node: "r"}}}},
			PhysicalVolumes: []PhysicalVolume{pv},
		}
		if _, err := PlanBest(in); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("tree case %d err=%v", i, err)
		}
	}

	in := Input{
		LogicalVolumes:  []LogicalVolume{{Candidates: []Candidate{{PhysicalVolume: "missing", Node: "r"}}}},
		PhysicalVolumes: []PhysicalVolume{{ID: "A", Nodes: []TreeNode{{ID: "r"}}}},
	}
	if _, err := PlanBest(in); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown physical volume err=%v", err)
	}

	in = Input{
		LogicalVolumes:  []LogicalVolume{{Candidates: []Candidate{{PhysicalVolume: "A", Node: "nope"}}}},
		PhysicalVolumes: []PhysicalVolume{{ID: "A", Nodes: []TreeNode{{ID: "r"}}}},
	}
	if _, err := PlanBest(in); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown node err=%v", err)
	}

	in = Input{
		LogicalVolumes:  []LogicalVolume{{Candidates: []Candidate{{PhysicalVolume: "A", Node: "r"}}}},
		PhysicalVolumes: []PhysicalVolume{{ID: "A", Nodes: []TreeNode{{ID: "r", Marks: []Mark{{Txn: 7, Volume: 0}}}}}},
	}
	if _, err := PlanBest(in); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown marked transaction err=%v", err)
	}
}

func TestSixtyVolumesAndHugeRank(t *testing.T) {
	in := Input{
		BlockCosts: map[BlockID]*big.Int{"shared": big.NewInt(3)},
	}
	const n = 60
	for v := 0; v < n; v++ {
		lv := LogicalVolume{Name: fmt.Sprintf("lv%d", v)}
		for c := 0; c < MaxCandidatesPerVolume; c++ {
			pv := PhysicalVolumeID(fmt.Sprintf("pv-%d-%d", v, c))
			in.PhysicalVolumes = append(in.PhysicalVolumes, PhysicalVolume{
				ID: pv, Nodes: []TreeNode{{ID: "r", Digest: "d"}},
			})
			lv.Candidates = append(lv.Candidates, Candidate{
				ID: fmt.Sprintf("c%d", c), PhysicalVolume: pv, Node: "r",
				Blocks: []BlockID{"shared"},
			})
		}
		in.LogicalVolumes = append(in.LogicalVolumes, lv)
	}

	res, err := PlanBest(in)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Feasible {
		t.Fatal("expected feasible")
	}
	want := new(big.Int).Exp(big.NewInt(4), big.NewInt(n), nil)
	if res.Count.Cmp(want) != 0 {
		t.Fatalf("count=%v, want %v", res.Count, want)
	}
	if res.Boundary.MaxItems != 1 || res.Boundary.MaxBlocks != 1 {
		t.Fatalf("unexpected frontier width for disjoint volumes: %+v", res.Boundary)
	}
	if res.DownloadCost.Cmp(big.NewInt(3)) != 0 {
		t.Fatalf("shared block cost=%v, want 3", res.DownloadCost)
	}
	for _, ch := range res.Certificate.Choices {
		if ch.Candidate != 0 {
			t.Fatalf("rank 1 chose candidate %d", ch.Candidate)
		}
	}

	last, err := Plan(in, new(big.Int).Set(want))
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range last.Certificate.Choices {
		if ch.Candidate != MaxCandidatesPerVolume-1 {
			t.Fatalf("last rank chose candidate %d", ch.Candidate)
		}
	}
}

func TestBoundaryWidthRejected(t *testing.T) {
	in := Input{
		LogicalVolumes: []LogicalVolume{
			{Candidates: []Candidate{{PhysicalVolume: "A", Node: "r"}}},
			{Candidates: []Candidate{{PhysicalVolume: "B", Node: "r"}}},
		},
		PhysicalVolumes: []PhysicalVolume{
			{ID: "A", Nodes: []TreeNode{{ID: "r"}}},
			{ID: "B", Nodes: []TreeNode{{ID: "r"}}},
		},
	}
	for i := 1; i <= DefaultMaxBoundaryItems; i++ {
		in.Transactions = append(in.Transactions, Transaction{
			ID: TxnID(i), Participants: []int{0, 1}, Benefit: big.NewInt(1),
		})
	}
	if _, err := PlanBest(in); err != nil {
		t.Fatalf("width at the limit must be accepted: %v", err)
	}
	in.Transactions = append(in.Transactions, Transaction{
		ID: TxnID(DefaultMaxBoundaryItems + 1), Participants: []int{0, 1}, Benefit: big.NewInt(1),
	})
	_, err := PlanBest(in)
	if !errors.Is(err, ErrBoundaryWidth) {
		t.Fatalf("err=%v, want ErrBoundaryWidth", err)
	}
}

// --- brute-force cross-check -------------------------------------------------

type bruteResult struct {
	feasible bool
	benefit  *big.Int
	cost     *big.Int
	count    *big.Int
	combos   [][]int
}

func brutePlan(in Input) bruteResult {
	n := len(in.LogicalVolumes)
	txnIndex := make(map[TxnID]int, len(in.Transactions))
	for i, tx := range in.Transactions {
		txnIndex[tx.ID] = i
	}
	var combos [][]int
	sel := make([]int, n)
	var rec func(int)
	rec = func(v int) {
		if v == n {
			combo := append([]int(nil), sel...)
			usedPV := make(map[PhysicalVolumeID]bool)
			for i, ci := range combo {
				pv := in.LogicalVolumes[i].Candidates[ci].PhysicalVolume
				if usedPV[pv] {
					return
				}
				usedPV[pv] = true
			}
			committed := make([]bool, len(in.Transactions))
			for ti, tx := range in.Transactions {
				ok := true
				for lv := 0; lv < n; lv++ {
					c := in.LogicalVolumes[lv].Candidates[combo[lv]]
					marks, _ := bruteCandidatePath(in, c)
					own, wrong := 0, 0
					for _, m := range marks {
						if m.Txn != tx.ID {
							continue
						}
						if m.Volume == lv {
							own++
						} else {
							wrong++
						}
					}
					expected := false
					for _, p := range tx.Participants {
						if p == lv {
							expected = true
							break
						}
					}
					if expected {
						if own != 1 || wrong != 0 {
							ok = false
							break
						}
					} else if own != 0 || wrong != 0 {
						ok = false
						break
					}
				}
				committed[ti] = ok
			}
			for _, id := range in.Required {
				ti := txnIndex[id]
				if !committed[ti] {
					return
				}
			}
			for ti, tx := range in.Transactions {
				if !committed[ti] {
					continue
				}
				for _, pre := range tx.Prerequisites {
					if !committed[txnIndex[pre]] {
						return
					}
				}
			}
			combos = append(combos, combo)
			return
		}
		for ci := range in.LogicalVolumes[v].Candidates {
			sel[v] = ci
			rec(v + 1)
		}
	}
	rec(0)
	if len(combos) == 0 {
		return bruteResult{feasible: false, benefit: new(big.Int), cost: new(big.Int), count: new(big.Int)}
	}

	// Recompute values for legal combos and retain only the optimum.
	bestB := new(big.Int)
	bestC := new(big.Int)
	first := true
	var bestCombos [][]int
	for _, combo := range combos {
		b, c := bruteComboValue(in, combo)
		if first {
			bestB.Set(b)
			bestC.Set(c)
			bestCombos = [][]int{combo}
			first = false
			continue
		}
		cmp := b.Cmp(bestB)
		if cmp > 0 || (cmp == 0 && c.Cmp(bestC) < 0) {
			bestB.Set(b)
			bestC.Set(c)
			bestCombos = [][]int{combo}
		} else if cmp == 0 && c.Cmp(bestC) == 0 {
			bestCombos = append(bestCombos, combo)
		}
	}
	return bruteResult{
		feasible: true,
		benefit:  bestB,
		cost:     bestC,
		count:    big.NewInt(int64(len(bestCombos))),
		combos:   bestCombos,
	}
}

func bruteComboValue(in Input, combo []int) (*big.Int, *big.Int) {
	committed := bruteCommitted(in, combo)
	benefit := new(big.Int)
	for ti, tx := range in.Transactions {
		if committed[ti] && tx.Benefit != nil {
			benefit.Add(benefit, tx.Benefit)
		}
	}
	blocks := make(map[BlockID]bool)
	for lv, ci := range combo {
		c := in.LogicalVolumes[lv].Candidates[ci]
		_, bs := bruteCandidatePath(in, c)
		for _, b := range bs {
			blocks[b] = true
		}
	}
	cost := new(big.Int)
	for b := range blocks {
		if c := in.BlockCosts[b]; c != nil {
			cost.Add(cost, c)
		}
	}
	return benefit, cost
}

func bruteCommitted(in Input, combo []int) []bool {
	committed := make([]bool, len(in.Transactions))
	for ti, tx := range in.Transactions {
		ok := true
		for lv := 0; lv < len(in.LogicalVolumes); lv++ {
			c := in.LogicalVolumes[lv].Candidates[combo[lv]]
			marks, _ := bruteCandidatePath(in, c)
			own, wrong := 0, 0
			for _, m := range marks {
				if m.Txn != tx.ID {
					continue
				}
				if m.Volume == lv {
					own++
				} else {
					wrong++
				}
			}
			expected := false
			for _, p := range tx.Participants {
				if p == lv {
					expected = true
					break
				}
			}
			if expected {
				if own != 1 || wrong != 0 {
					ok = false
					break
				}
			} else if own != 0 || wrong != 0 {
				ok = false
				break
			}
		}
		committed[ti] = ok
	}
	return committed
}

func bruteCandidatePath(in Input, c Candidate) ([]Mark, []BlockID) {
	var pv *PhysicalVolume
	for i := range in.PhysicalVolumes {
		if in.PhysicalVolumes[i].ID == c.PhysicalVolume {
			pv = &in.PhysicalVolumes[i]
			break
		}
	}
	if pv == nil {
		panic("missing pv")
	}
	nodes := make(map[NodeID]TreeNode, len(pv.Nodes))
	for _, n := range pv.Nodes {
		nodes[n.ID] = n
	}
	var chain []TreeNode
	id := c.Node
	for id != "" {
		n := nodes[id]
		chain = append(chain, n)
		id = n.Parent
	}
	var marks []Mark
	var blocks []BlockID
	seen := make(map[BlockID]bool)
	for i := len(chain) - 1; i >= 0; i-- {
		marks = append(marks, chain[i].Marks...)
		for _, b := range chain[i].Blocks {
			if !seen[b] {
				seen[b] = true
				blocks = append(blocks, b)
			}
		}
	}
	for _, b := range c.Blocks {
		if !seen[b] {
			seen[b] = true
			blocks = append(blocks, b)
		}
	}
	return marks, blocks
}

func TestRandomSmallAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20261121))
	for iter := 0; iter < 1000; iter++ {
		in := randomInput(rng)
		in.MaxBoundaryItems = 64
		want := brutePlan(in)
		res, err := PlanBest(in)
		if err != nil {
			t.Fatalf("iter %d: unexpected error: %v\ninput=%+v", iter, err, in)
		}
		if res.Feasible != want.feasible {
			t.Fatalf("iter %d: feasible=%v want %v\ninput=%+v", iter, res.Feasible, want.feasible, in)
		}
		if !want.feasible {
			continue
		}
		if res.Benefit.Cmp(want.benefit) != 0 || res.DownloadCost.Cmp(want.cost) != 0 {
			t.Fatalf("iter %d: got (benefit %v, cost %v), want (%v, %v)\ninput=%+v",
				iter, res.Benefit, res.DownloadCost, want.benefit, want.cost, in)
		}
		if res.Count.Cmp(want.count) != 0 {
			t.Fatalf("iter %d: count=%v want %v\ninput=%+v", iter, res.Count, want.count, in)
		}
		checkRank := func(rank int64) {
			rr, err := PlanRank(in, rank)
			if err != nil {
				t.Fatalf("iter %d rank %d: %v", iter, rank, err)
			}
			wantCombo := want.combos[rank-1]
			for v, ch := range rr.Certificate.Choices {
				if ch.Candidate != wantCombo[v] {
					t.Fatalf("iter %d rank %d volume %d: candidate %d want %d\ninput=%+v",
						iter, rank, v, ch.Candidate, wantCombo[v], in)
				}
			}
			if rr.Benefit.Cmp(want.benefit) != 0 || rr.DownloadCost.Cmp(want.cost) != 0 {
				t.Fatalf("iter %d rank %d: ranked value mismatch", iter, rank)
			}
			committed := bruteCommitted(in, wantCombo)
			var expected []TxnID
			for ti, tx := range in.Transactions {
				if committed[ti] {
					expected = append(expected, tx.ID)
				}
			}
			if len(expected) != len(rr.Certificate.Committed) {
				t.Fatalf("iter %d rank %d: committed %v want %v",
					iter, rank, rr.Certificate.Committed, expected)
			}
			for k := range expected {
				if expected[k] != rr.Certificate.Committed[k] {
					t.Fatalf("iter %d rank %d: committed %v want %v",
						iter, rank, rr.Certificate.Committed, expected)
				}
			}
		}
		checkRank(1)
		if want.count.Int64() >= 2 {
			checkRank(want.count.Int64())
			checkRank(1 + rng.Int63n(want.count.Int64()))
		}
	}
}

func randomInput(rng *rand.Rand) Input {
	n := 1 + rng.Intn(5)
	in := Input{LogicalVolumes: make([]LogicalVolume, n)}

	tCount := rng.Intn(5)
	for i := 0; i < tCount; i++ {
		var participants []int
		for v := 0; v < n; v++ {
			if rng.Intn(3) != 0 {
				participants = append(participants, v)
			}
		}
		if len(participants) == 0 {
			participants = []int{rng.Intn(n)}
		}
		tx := Transaction{
			ID:           TxnID(i + 1),
			Participants: participants,
			Benefit:      big.NewInt(int64(rng.Intn(9) - 3)),
		}
		if tCount > 1 && rng.Intn(4) == 0 {
			tx.Prerequisites = []TxnID{TxnID(1 + rng.Intn(tCount))}
		}
		in.Transactions = append(in.Transactions, tx)
	}
	if tCount > 0 && rng.Intn(3) == 0 {
		in.Required = []TxnID{TxnID(1 + rng.Intn(tCount))}
	}

	pvCount := n + 1 + rng.Intn(3)
	blockIDs := []BlockID{"b0", "b1", "b2", "b3", "b4"}
	for pi := 0; pi < pvCount; pi++ {
		nodeCount := 1 + rng.Intn(4)
		pv := PhysicalVolume{
			ID:         PhysicalVolumeID(fmt.Sprintf("pv%d", pi)),
			Generation: uint64(rng.Intn(5)),
		}
		for ni := 0; ni < nodeCount; ni++ {
			node := TreeNode{
				ID:         NodeID(fmt.Sprintf("n%d", ni)),
				Generation: uint64(rng.Intn(5)),
				Digest:     fmt.Sprintf("d%d-%d", pi, ni),
			}
			if ni > 0 {
				node.Parent = NodeID(fmt.Sprintf("n%d", rng.Intn(ni)))
			}
			if tCount > 0 {
				for k := 0; k < rng.Intn(3); k++ {
					if rng.Intn(2) == 0 {
						node.Marks = append(node.Marks, Mark{
							Txn:    TxnID(1 + rng.Intn(tCount)),
							Volume: rng.Intn(n),
						})
					}
				}
			}
			if rng.Intn(5) == 0 {
				node.Blocks = append(node.Blocks, blockIDs[rng.Intn(len(blockIDs))])
			}
			pv.Nodes = append(pv.Nodes, node)
		}
		in.PhysicalVolumes = append(in.PhysicalVolumes, pv)
	}
	for v := 0; v < n; v++ {
		candCount := 1 + rng.Intn(3)
		for ci := 0; ci < candCount; ci++ {
			pv := in.PhysicalVolumes[rng.Intn(len(in.PhysicalVolumes))]
			node := pv.Nodes[rng.Intn(len(pv.Nodes))]
			c := Candidate{
				ID:             fmt.Sprintf("c%d", ci),
				PhysicalVolume: pv.ID,
				Node:           node.ID,
			}
			if rng.Intn(3) == 0 {
				c.Blocks = append(c.Blocks, blockIDs[rng.Intn(len(blockIDs))])
			}
			in.LogicalVolumes[v].Candidates = append(in.LogicalVolumes[v].Candidates, c)
		}
	}
	in.BlockCosts = make(map[BlockID]*big.Int)
	for _, b := range blockIDs {
		if rng.Intn(5) != 0 {
			in.BlockCosts[b] = big.NewInt(int64(rng.Intn(6)))
		}
	}
	return in
}
