package main

import (
	"math/big"
	"testing"
)

// Exhaustive domain enumeration must confirm exact probabilities:
// P(first = i) = w_i/W and P(second = j | first = i) = w_j/(W-w_i).
func TestEnumerateExactProbabilities(t *testing.T) {
	cases := [][]int64{
		{1, 2, 3},
		{1, 1, 1, 1},
		{2, 7, 3, 11, 5},
		{0, 4, 0, 6}, // zero-weight blocks: count 0, never selected
	}
	for _, c := range cases {
		rep, err := Enumerate(bigWeights(c...), 10_000_000)
		if err != nil {
			t.Fatalf("weights %v: %v", c, err)
		}
		if !rep.FirstOK {
			t.Errorf("weights %v: first-draw counts deviate from exact probabilities", c)
		}
		if !rep.PairsOK {
			t.Errorf("weights %v: second-draw pair counts deviate from exact probabilities", c)
		}
		for i, wi := range c {
			if rep.FirstCounts[i] != wi {
				t.Errorf("weights %v: block %d first count %d, want %d", c, i, rep.FirstCounts[i], wi)
			}
			if rep.PairCounts[i][i] != 0 {
				t.Errorf("weights %v: block %d selected twice in %d pairs", c, i, rep.PairCounts[i][i])
			}
		}
	}
}

func TestEnumerateRejectsHugeArchive(t *testing.T) {
	huge := new(big.Int).Lsh(big.NewInt(1), 100)
	_, err := Enumerate([]*big.Int{huge, big.NewInt(1)}, 10_000_000)
	if err == nil {
		t.Fatal("expected error for archive too large to enumerate")
	}
}

func TestEnumerateRejectsExcessiveWork(t *testing.T) {
	// Total weight 20000 -> pair work ~ 20000*20000 = 4e8 > limit.
	w := make([]*big.Int, 0)
	for i := 0; i < 20; i++ {
		w = append(w, big.NewInt(1000))
	}
	_, err := Enumerate(w, 1_000_000)
	if err == nil {
		t.Fatal("expected error for excessive enumeration work")
	}
}
