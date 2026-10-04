package intersect

import (
	"math/rand"
	"strings"
	"testing"
)

func blocksOf(size int, ids ...uint64) []Block {
	norm := Normalize(ids)
	if size <= 0 {
		size = 1
	}
	var out []Block
	for i := 0; i < len(norm); i += size {
		end := i + size
		if end > len(norm) {
			end = len(norm)
		}
		out = append(out, NewBlock(norm[i:end]))
	}
	if len(out) == 0 {
		out = append(out, NewBlock(nil))
	}
	return out
}

func mustIntersect(t *testing.T, lists ...[]Block) ([]uint64, Proof) {
	t.Helper()
	res, proof, err := Intersect(lists)
	if err != nil {
		t.Fatalf("Intersect: %v", err)
	}
	if err := Verify(lists, proof); err != nil {
		t.Fatalf("Verify rejected honest proof: %v", err)
	}
	return res, proof
}

func dispositions(p Proof) map[Disposition]int {
	m := make(map[Disposition]int)
	for _, ev := range p.Evidence {
		m[ev.Disposition]++
	}
	return m
}

func TestBasicIntersection(t *testing.T) {
	a := blocksOf(2, 1, 2, 3, 4, 5, 6)
	b := blocksOf(2, 2, 4, 6, 8)
	c := blocksOf(3, 4, 6, 7)
	res, proof := mustIntersect(t, a, b, c)
	if len(res) != 2 || res[0] != 4 || res[1] != 6 {
		t.Fatalf("got %v, want [4 6]", res)
	}
	if proof.Lower != 4 || proof.Upper != 6 {
		t.Fatalf("bounds [%d,%d], want [4,6]", proof.Lower, proof.Upper)
	}
	for _, ev := range proof.Evidence {
		if ev.Disposition != Read && ev.Reason == "" {
			t.Fatalf("skipped block %d/%d has no justification", ev.ListIndex, ev.BlockIndex)
		}
	}
}

func TestEmptyList(t *testing.T) {
	empty := []Block{NewBlock(nil)}
	full := blocksOf(2, 1, 2, 3)
	res, proof := mustIntersect(t, full, empty)
	if len(res) != 0 {
		t.Fatalf("got %v, want empty", res)
	}
	if !proof.EmptyList {
		t.Fatal("proof should declare empty list")
	}
	if d := dispositions(proof); d[Read] != 0 {
		t.Fatal("no block should be read when a list is empty")
	}
}

func TestEmptyBlocksOnly(t *testing.T) {
	a := []Block{NewBlock(nil), NewBlock(nil)}
	b := blocksOf(2, 5, 6)
	res, proof := mustIntersect(t, a, b)
	if len(res) != 0 || !proof.EmptyList {
		t.Fatalf("got %v emptyList=%v", res, proof.EmptyList)
	}
}

func TestDuplicateNormalization(t *testing.T) {
	blk := NewBlock([]uint64{5, 3, 3, 1, 5, 2, 1})
	want := []uint64{1, 2, 3, 5}
	if !equalIDs(blk.IDs, want) {
		t.Fatalf("normalized %v, want %v", blk.IDs, want)
	}
	if blk.Summary.Count != 4 || blk.Summary.Min != 1 || blk.Summary.Max != 5 {
		t.Fatalf("bad summary %+v", blk.Summary)
	}
	// Duplicates in inputs must not duplicate results or break proofs.
	a := []Block{NewBlock([]uint64{3, 3, 3, 7, 7})}
	b := []Block{NewBlock([]uint64{3, 7, 7, 7, 9})}
	res, proof := mustIntersect(t, a, b)
	if !equalIDs(res, []uint64{3, 7}) {
		t.Fatalf("got %v, want [3 7]", res)
	}
	if d := dispositions(proof); d[Read] != 2 {
		t.Fatalf("want 2 read blocks, got %v", d)
	}
}

// A block whose max equals the lower bound (or whose min equals the
// upper bound) sits exactly on the boundary: the skip test is strict,
// so such blocks must be read, and the boundary ID must be found.
func TestBoundaryExactlyEqual(t *testing.T) {
	a := []Block{NewBlock([]uint64{1, 2, 3}), NewBlock([]uint64{4, 5, 6})}
	b := []Block{NewBlock([]uint64{3}), NewBlock([]uint64{6, 7})}
	// lower = max(1,3) = 3, upper = min(6,7) = 6.
	// a[0] max=3 == lower: must be read. b[1] min=6 == upper: must be read.
	res, proof := mustIntersect(t, a, b)
	if !equalIDs(res, []uint64{3, 6}) {
		t.Fatalf("got %v, want [3 6]", res)
	}
	for _, ev := range proof.Evidence {
		if ev.Disposition != Read {
			t.Fatalf("block %d/%d (%+v) on exact boundary must be read, got %v",
				ev.ListIndex, ev.BlockIndex, ev.Summary, ev.Disposition)
		}
	}
}

// The mirror image: blocks strictly outside the window are skipped,
// and a block ending exactly at lower-1 (or starting at upper+1) is
// the last skippable one.
func TestSkipJustBelowAndAbove(t *testing.T) {
	a := []Block{
		NewBlock([]uint64{1, 2}),   // max 2 < lower: skipped-below
		NewBlock([]uint64{3, 4}),   // max 4 == lower: read
		NewBlock([]uint64{5, 6}),   // inside: read
		NewBlock([]uint64{8, 9}),   // min 8 == upper+1: skipped-above
	}
	b := []Block{NewBlock([]uint64{4, 5, 6, 7})} // window [4,7]
	res, proof := mustIntersect(t, a, b)
	if !equalIDs(res, []uint64{4, 5, 6}) {
		t.Fatalf("got %v, want [4 5 6]", res)
	}
	d := dispositions(proof)
	if d[SkippedBelow] != 1 || d[SkippedAbove] != 1 || d[Read] != 3 {
		t.Fatalf("dispositions %v, want 1 below / 1 above / 3 read", d)
	}
	for _, ev := range proof.Evidence {
		if ev.Disposition == SkippedBelow && !strings.Contains(ev.Reason, "lower bound 4") {
			t.Fatalf("skip-below reason lacks basis: %q", ev.Reason)
		}
		if ev.Disposition == SkippedAbove && !strings.Contains(ev.Reason, "upper bound 7") {
			t.Fatalf("skip-above reason lacks basis: %q", ev.Reason)
		}
	}
}

func TestNoOverlapEmptyResult(t *testing.T) {
	a := blocksOf(2, 1, 2, 3)
	b := blocksOf(2, 10, 11, 12)
	res, proof := mustIntersect(t, a, b)
	if len(res) != 0 {
		t.Fatalf("got %v, want empty", res)
	}
	if proof.Lower != 10 || proof.Upper != 3 {
		t.Fatalf("bounds [%d,%d], want inverted [10,3]", proof.Lower, proof.Upper)
	}
	if d := dispositions(proof); d[Read] != 0 {
		t.Fatalf("inverted window: nothing should be read, got %v", d)
	}
}

func TestTamperedEvidenceSummaryFails(t *testing.T) {
	lists := [][]Block{blocksOf(2, 1, 2, 3, 4), blocksOf(2, 2, 4)}
	_, proof, err := Intersect(lists)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		mutate func(*Proof)
	}{
		{"hash", func(p *Proof) { p.Evidence[0].Summary.Hash[0] ^= 0xff }},
		{"count", func(p *Proof) { p.Evidence[0].Summary.Count++ }},
		{"min", func(p *Proof) { p.Evidence[0].Summary.Min++ }},
		{"max", func(p *Proof) { p.Evidence[0].Summary.Max-- }},
	}
	for _, tc := range cases {
		tampered := proof
		tampered.Evidence = append([]Evidence(nil), proof.Evidence...)
		tc.mutate(&tampered)
		if err := Verify(lists, tampered); err == nil {
			t.Fatalf("tampered summary field %s: Verify succeeded", tc.name)
		}
	}
}

func TestTamperedStoredSummaryFails(t *testing.T) {
	lists := [][]Block{blocksOf(2, 1, 2, 3, 4), blocksOf(2, 2, 4)}
	_, proof, err := Intersect(lists)
	if err != nil {
		t.Fatal(err)
	}
	lists[0][0].Summary.Count++
	if err := Verify(lists, proof); err == nil {
		t.Fatal("tampered stored block summary: Verify succeeded")
	}
}

func TestTamperedResultFails(t *testing.T) {
	lists := [][]Block{blocksOf(2, 1, 2, 3, 4), blocksOf(2, 2, 4)}
	_, proof, err := Intersect(lists)
	if err != nil {
		t.Fatal(err)
	}
	missing := proof
	missing.Result = proof.Result[:len(proof.Result)-1] // drop a hit
	if err := Verify(lists, missing); err == nil {
		t.Fatal("dropped result item: Verify succeeded")
	}
	extra := proof
	extra.Result = append(append([]uint64(nil), proof.Result...), 3)
	if err := Verify(lists, extra); err == nil {
		t.Fatal("added non-hit: Verify succeeded")
	}
}

func TestUnjustifiedSkipFails(t *testing.T) {
	lists := [][]Block{blocksOf(2, 1, 2, 3, 4), blocksOf(2, 2, 4)}
	_, proof, err := Intersect(lists)
	if err != nil {
		t.Fatal(err)
	}
	// Flip a genuinely-read block to skipped-below: verifier must
	// reject the justification and/or the recomputed result.
	for i := range proof.Evidence {
		if proof.Evidence[i].Disposition == Read {
			proof.Evidence[i].Disposition = SkippedBelow
			proof.Evidence[i].Reason = "lied"
			break
		}
	}
	if err := Verify(lists, proof); err == nil {
		t.Fatal("unjustified skip: Verify succeeded")
	}
}

func TestHiddenBlockFails(t *testing.T) {
	lists := [][]Block{blocksOf(2, 1, 2, 3, 4), blocksOf(2, 2, 4)}
	_, proof, err := Intersect(lists)
	if err != nil {
		t.Fatal(err)
	}
	proof.Evidence = proof.Evidence[1:] // hide one block
	if err := Verify(lists, proof); err == nil {
		t.Fatal("hidden block: Verify succeeded")
	}
}

func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		nLists := 2 + rng.Intn(3)
		lists := make([][]Block, nLists)
		naive := make(map[uint64]int)
		for i := range lists {
			n := rng.Intn(30)
			raw := make([]uint64, 0, n)
			for j := 0; j < n; j++ {
				raw = append(raw, uint64(rng.Intn(50)))
			}
			norm := Normalize(raw)
			for _, id := range norm {
				naive[id]++
			}
			size := 1 + rng.Intn(5)
			lists[i] = blocksOf(size, norm...)
		}
		var want []uint64
		for id, c := range naive {
			if c == nLists {
				want = append(want, id)
			}
		}
		want = Normalize(want)
		res, proof, err := Intersect(lists)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if !equalIDs(res, want) {
			t.Fatalf("trial %d: got %v, want %v", trial, res, want)
		}
		if err := Verify(lists, proof); err != nil {
			t.Fatalf("trial %d: verify: %v", trial, err)
		}
	}
}
