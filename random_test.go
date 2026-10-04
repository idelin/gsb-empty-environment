package main

import (
	"encoding/json"
	"math/rand"
	"testing"
)

// checkMapping validates the Tai mapping axioms: one-to-one, ancestor and
// sibling order preservation.
func checkMapping(t *testing.T, src, tgt *Tree, mapping [][2]int) {
	t.Helper()
	f1, f2 := flatten(src), flatten(tgt)
	seenS, seenT := map[int]bool{}, map[int]bool{}
	anc := func(ft *flatTree, a, b int) bool { // is a a proper ancestor of b?
		for p := ft.parent[b]; p != 0; p = ft.parent[p] {
			if p == a {
				return true
			}
		}
		return false
	}
	before := func(ft *flatTree, a, b int) bool { // a left of b in preorder, unrelated
		if anc(ft, a, b) || anc(ft, b, a) {
			return false
		}
		ia, ib := -1, -1
		for i, id := range ft.preorder {
			if id == a {
				ia = i
			}
			if id == b {
				ib = i
			}
		}
		return ia < ib
	}
	for _, pr := range mapping {
		s, tg := pr[0], pr[1]
		if seenS[s] || seenT[tg] {
			t.Fatalf("mapping not one-to-one: %v in %v", pr, mapping)
		}
		seenS[s], seenT[tg] = true, true
		for _, qr := range mapping {
			u, v := qr[0], qr[1]
			if anc(f1, s, u) != anc(f2, tg, v) {
				t.Fatalf("ancestor order violated: %v vs %v", pr, qr)
			}
			if before(f1, s, u) != before(f2, tg, v) {
				t.Fatalf("sibling order violated: %v vs %v", pr, qr)
			}
		}
	}
}

func crossCheck(t *testing.T, seeds int, labels []string, maxNodes int) {
	t.Helper()
	for seed := int64(0); seed < int64(seeds); seed++ {
		r := rand.New(rand.NewSource(seed))
		src := randTree(r, labels, maxNodes)
		tgt := randTree(r, labels, maxNodes)
		c := randCosts(r, labels)
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
		nc := normalizeCosts(c, src, tgt)
		dist, mapping := treeEditDistance(src, tgt, nc)
		checkMapping(t, src, tgt, mapping)
		ops, err := buildScript(src, tgt, mapping, nc)
		if err != nil {
			t.Fatalf("seed %d: buildScript: %v", seed, err)
		}
		res, cost, err := ApplyScript(src, ops, c)
		if err != nil {
			t.Fatalf("seed %d: ApplyScript: %v", seed, err)
		}
		if cost != dist || !Equal(res, tgt) {
			sj, _ := json.Marshal(src)
			tj, _ := json.Marshal(tgt)
			t.Fatalf("seed %d: cost=%d dist=%d equal=%v\nsrc=%s\ntgt=%s\nops=%v",
				seed, cost, dist, Equal(res, tgt), sj, tj, ops)
		}
		ed, _, ok := ExhaustiveDistance(src, tgt, c, 3000000)
		if !ok {
			t.Fatalf("seed %d: exhaustive inconclusive", seed)
		}
		if ed != dist {
			sj, _ := json.Marshal(src)
			tj, _ := json.Marshal(tgt)
			cj, _ := json.Marshal(c)
			t.Fatalf("seed %d: DP=%d exhaustive=%d\nsrc=%s\ntgt=%s\ncosts=%s",
				seed, dist, ed, sj, tj, cj)
		}
	}
}

func TestRandomCrossCheck2Labels(t *testing.T) {
	crossCheck(t, 150, []string{"a", "b"}, 4)
}

func TestRandomCrossCheck3Labels(t *testing.T) {
	crossCheck(t, 40, []string{"a", "b", "c"}, 3)
}
