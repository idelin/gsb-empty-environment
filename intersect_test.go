package intersect

import (
	"reflect"
	"strings"
	"testing"
)

func blk(ids ...string) Block { return Block{IDs: ids} }

func mustProve(t *testing.T, lists [][]Block) (Proof, [32]byte) {
	t.Helper()
	proof, root, err := Prove(lists)
	if err != nil {
		t.Fatalf("Prove: %v", err)
	}
	got, err := Verify(root, proof)
	if err != nil {
		t.Fatalf("Verify(valid proof): %v", err)
	}
	if !reflect.DeepEqual(normalize(got), normalize(proof.Result)) {
		t.Fatalf("Verify returned %v, proof claims %v", got, proof.Result)
	}
	return proof, root
}

func normalize(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}

func TestBasicIntersectionWithSkips(t *testing.T) {
	lists := [][]Block{
		{blk("a", "b"), blk("c", "h", "y", "z")},
		{blk("c", "d"), blk("h", "i"), blk("y", "z")},
		{blk("c", "h", "y", "z"), blk("za", "zb")},
	}
	proof, _ := mustProve(t, lists)
	want := []string{"c", "h", "y", "z"}
	if !reflect.DeepEqual(proof.Result, want) {
		t.Fatalf("result = %v, want %v", proof.Result, want)
	}

	// list 0 block 0: max "b" < feasible lo "c" -> skipped below.
	bp := proof.Lists[0].Blocks[0]
	if bp.Provided || bp.Reason != ReasonBelowRange {
		t.Fatalf("list0 block0: provided=%v reason=%q, want skipped/%q", bp.Provided, bp.Reason, ReasonBelowRange)
	}
	if bp.Detail == "" || !strings.Contains(bp.Detail, `"b"`) || !strings.Contains(bp.Detail, `"c"`) {
		t.Fatalf("skip detail must explain the basis, got %q", bp.Detail)
	}
	// list 2 block 1: min "za" > feasible hi "z" -> skipped above.
	bp = proof.Lists[2].Blocks[1]
	if bp.Provided || bp.Reason != ReasonAboveRange {
		t.Fatalf("list2 block1: provided=%v reason=%q, want skipped/%q", bp.Provided, bp.Reason, ReasonAboveRange)
	}
	// Every other block overlaps the feasible range and must be provided.
	for i, lp := range proof.Lists {
		for j, bp := range lp.Blocks {
			if (i == 0 && j == 0) || (i == 2 && j == 1) {
				continue
			}
			if !bp.Provided {
				t.Fatalf("list %d block %d should be provided, reason=%q", i, j, bp.Reason)
			}
		}
	}
}

func TestEmptyList(t *testing.T) {
	// A list with no blocks at all makes the intersection provably empty.
	lists := [][]Block{
		{},
		{blk("a", "b"), blk("c", "d")},
	}
	proof, _ := mustProve(t, lists)
	if len(proof.Result) != 0 {
		t.Fatalf("result = %v, want empty", proof.Result)
	}
	for i, lp := range proof.Lists {
		for j, bp := range lp.Blocks {
			if bp.Provided || bp.Reason != ReasonInfeasibleRange {
				t.Fatalf("list %d block %d: provided=%v reason=%q, want skipped/%q",
					i, j, bp.Provided, bp.Reason, ReasonInfeasibleRange)
			}
		}
	}

	// A list whose blocks are all empty is also provably empty.
	lists = [][]Block{
		{Block{IDs: nil}},
		{blk("a")},
	}
	proof, _ = mustProve(t, lists)
	if len(proof.Result) != 0 {
		t.Fatalf("result = %v, want empty", proof.Result)
	}

	// An empty block inside an otherwise usable list is skipped as empty.
	lists = [][]Block{
		{Block{IDs: nil}, blk("a", "b")},
		{blk("b")},
	}
	proof, _ = mustProve(t, lists)
	if !reflect.DeepEqual(proof.Result, []string{"b"}) {
		t.Fatalf("result = %v, want [b]", proof.Result)
	}
	if bp := proof.Lists[0].Blocks[0]; bp.Provided || bp.Reason != ReasonEmptyBlock {
		t.Fatalf("empty block: provided=%v reason=%q, want skipped/%q", bp.Provided, bp.Reason, ReasonEmptyBlock)
	}
}

func TestDuplicateNormalization(t *testing.T) {
	lists := [][]Block{
		{blk("a", "a", "b", "b", "c"), blk("c", "c", "d")},
		{blk("b", "b", "c", "c")},
	}
	proof, _ := mustProve(t, lists)
	want := []string{"b", "c"}
	if !reflect.DeepEqual(proof.Result, want) {
		t.Fatalf("result = %v, want %v (duplicates must be normalized)", proof.Result, want)
	}
	// Summaries still commit to the raw contents, duplicates included.
	if got := proof.Lists[0].Blocks[0].Summary.Count; got != 5 {
		t.Fatalf("summary count = %d, want 5 (raw count including duplicates)", got)
	}
}

func TestSeekBoundaryExactlyEqual(t *testing.T) {
	summaries := make([]BlockSummary, 0)
	for _, ids := range [][]string{{"a", "b"}, {"c", "d"}, {"e", "f"}} {
		s, err := Summarize(blk(ids...))
		if err != nil {
			t.Fatal(err)
		}
		summaries = append(summaries, s)
	}
	cases := []struct {
		target string
		want   int
	}{
		{"a", 0}, // before first max
		{"b", 0}, // exactly equal to block 0 max: must NOT jump past it
		{"c", 1},
		{"d", 1}, // exactly equal to block 1 max
		{"e", 2},
		{"f", 2}, // exactly equal to last max
		{"g", 3}, // beyond everything
	}
	for _, c := range cases {
		if got := firstBlockAtOrAfter(summaries, c.target); got != c.want {
			t.Fatalf("firstBlockAtOrAfter(%q) = %d, want %d", c.target, got, c.want)
		}
	}

	// End to end: feasible lo lands exactly on a block's max, so the block
	// must be provided rather than skipped.
	lists := [][]Block{
		{blk("a", "b")},
		{blk("b")},
	}
	proof, _ := mustProve(t, lists)
	if !reflect.DeepEqual(proof.Result, []string{"b"}) {
		t.Fatalf("result = %v, want [b]", proof.Result)
	}
	if bp := proof.Lists[0].Blocks[0]; !bp.Provided {
		t.Fatalf("block whose max equals the feasible lower bound must be provided, got reason=%q", bp.Reason)
	}
}

func TestRangeOverlapAloneDoesNotProveHits(t *testing.T) {
	// Ranges overlap ([a,c] vs [b,d]) yet the lists share no ID. The proof
	// must include the full contents of both blocks; range overlap is not
	// evidence of a hit, and an empty result still needs verification.
	lists := [][]Block{
		{blk("a", "c")},
		{blk("b", "d")},
	}
	proof, _ := mustProve(t, lists)
	if len(proof.Result) != 0 {
		t.Fatalf("result = %v, want empty", proof.Result)
	}
	for i, lp := range proof.Lists {
		for j, bp := range lp.Blocks {
			if !bp.Provided {
				t.Fatalf("list %d block %d: overlapping block must be provided, reason=%q", i, j, bp.Reason)
			}
		}
	}
}

func cloneProof(p Proof) Proof {
	out := Proof{Result: append([]string(nil), p.Result...), Lists: make([]ListProof, len(p.Lists))}
	for i, lp := range p.Lists {
		blocks := make([]BlockProof, len(lp.Blocks))
		for j, bp := range lp.Blocks {
			blocks[j] = bp
			blocks[j].IDs = append([]string(nil), bp.IDs...)
		}
		out.Lists[i] = ListProof{Blocks: blocks}
	}
	return out
}

func TestTamperingFails(t *testing.T) {
	lists := [][]Block{
		{blk("a", "b"), blk("c", "h", "y", "z")},
		{blk("c", "d"), blk("h", "i"), blk("y", "z")},
		{blk("c", "h", "y", "z"), blk("za", "zb")},
	}
	proof, root := mustProve(t, lists)

	cases := []struct {
		name   string
		mutate func(p *Proof) (rootOverride *[32]byte)
	}{
		{"summary count tampered", func(p *Proof) *[32]byte {
			p.Lists[0].Blocks[1].Summary.Count++
			return nil
		}},
		{"summary hash tampered", func(p *Proof) *[32]byte {
			p.Lists[1].Blocks[0].Summary.Hash[0] ^= 0xff
			return nil
		}},
		{"summary min tampered", func(p *Proof) *[32]byte {
			p.Lists[0].Blocks[0].Summary.Min = "0"
			return nil
		}},
		{"provided block content tampered", func(p *Proof) *[32]byte {
			p.Lists[0].Blocks[1].IDs[0] = "cc"
			return nil
		}},
		{"result item removed", func(p *Proof) *[32]byte {
			p.Result = p.Result[1:]
			return nil
		}},
		{"result item added", func(p *Proof) *[32]byte {
			p.Result = append(p.Result, "zz")
			return nil
		}},
		{"overlapping block hidden", func(p *Proof) *[32]byte {
			bp := &p.Lists[0].Blocks[1]
			bp.Provided = false
			bp.IDs = nil
			bp.Reason = ReasonBelowRange
			return nil
		}},
		{"skip reason forged", func(p *Proof) *[32]byte {
			p.Lists[0].Blocks[0].Reason = ReasonAboveRange
			return nil
		}},
		{"block dropped from proof", func(p *Proof) *[32]byte {
			p.Lists[1].Blocks = p.Lists[1].Blocks[1:]
			return nil
		}},
		{"wrong trusted root", func(p *Proof) *[32]byte {
			bad := ManifestRoot([][]BlockSummary{{{Min: "x", Max: "x", Count: 1}}})
			return &bad
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := cloneProof(proof)
			r := root
			if override := c.mutate(&p); override != nil {
				r = *override
			}
			if _, err := Verify(r, p); err == nil {
				t.Fatalf("Verify accepted tampered proof (%s)", c.name)
			}
		})
	}
}

func TestUnsortedBlockRejected(t *testing.T) {
	if _, err := Summarize(blk("b", "a")); err == nil {
		t.Fatal("Summarize accepted an unsorted block")
	}
	if _, _, err := Prove([][]Block{{blk("b", "a")}}); err == nil {
		t.Fatal("Prove accepted an unsorted block")
	}
}
