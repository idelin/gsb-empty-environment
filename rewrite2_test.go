package netsec

import "testing"

// buildMul8Old: 8-bit signed multiply with explicit overflow error node 7.
func buildMul8Old(t *testing.T) *Graph {
	t.Helper()
	b := NewBuilder("mul8-old", 2)
	x := b.Input(0)
	y := b.Input(1)
	p := b.Binary(3, OpMul, x, y)
	ovf := b.Overflow(4, p)
	errBlk := b.NewBlock()
	okBlk := b.NewBlock()
	b.Branch(ovf, errBlk, okBlk)
	b.UseBlock(errBlk)
	b.Error(7)
	b.UseBlock(okBlk)
	return b.Return(p)
}

// buildMul8New hardens the multiply by widening to 16 bits and reconstructing
// the original 8-bit overflow predicate on the widened operation.
func buildMul8New(t *testing.T, wrongNode, dropCheck bool) *Graph {
	t.Helper()
	b := NewBuilder("mul8-new", 2)
	x := b.SExt(1, b.Input(0), 16)
	y := b.SExt(2, b.Input(1), 16)
	p := b.Binary(3, OpMul, x, y)
	low8 := b.Trunc(5, p, 8)
	if dropCheck {
		return b.Return(low8)
	}
	// 8-bit overflow iff the wide product differs from sign-ext(low8).
	reext := b.SExt(7, low8, 16)
	ovf := b.Compare(8, OpNe, p, reext)
	errBlk := b.NewBlock()
	okBlk := b.NewBlock()
	b.Branch(ovf, errBlk, okBlk)
	b.UseBlock(errBlk)
	node := 7
	if wrongNode {
		node = 99
	}
	b.Error(node)
	b.UseBlock(okBlk)
	return b.Return(low8)
}

func TestMul8Equivalent(t *testing.T) {
	oldG := buildMul8Old(t)
	newG := buildMul8New(t, false, false)

	// Ground truth first: the independent interpreter must agree on all
	// 256*256 inputs.
	mm, err := EnumerateAll(oldG, newG, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(mm) != 0 {
		t.Fatalf("interpreter found %d mismatches before symbolic check", len(mm))
	}

	res, err := Verify(oldG, newG, Config{Budget: 1_000_000})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.Status != StatusEquivalent {
		t.Fatalf("status=%v reason=%q witness=%+v", res.Status, res.Reason, res.Witness)
	}
}

func TestMul8WrongNode(t *testing.T) {
	res, err := Verify(buildMul8Old(t), buildMul8New(t, true, false), Config{Budget: 1_000_000})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.Status != StatusMismatch || res.Witness == nil {
		t.Fatalf("status=%v reason=%q, want mismatch", res.Status, res.Reason)
	}
	if res.Witness.Old.ErrNode != 7 || res.Witness.New.ErrNode != 99 {
		t.Fatalf("want error nodes 7 vs 99, got old=%+v new=%+v",
			res.Witness.Old, res.Witness.New)
	}
	// Witness must be independently reproducible.
	oo, _ := Interpret(buildMul8Old(t), res.Witness.Inputs)
	no, _ := Interpret(buildMul8New(t, true, false), res.Witness.Inputs)
	if outcomesEqual(oo, no) {
		t.Fatalf("witness not reproducible through interpreter")
	}
}

func TestMul8DroppedCheck(t *testing.T) {
	res, err := Verify(buildMul8Old(t), buildMul8New(t, false, true), Config{Budget: 1_000_000})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.Status != StatusMismatch || res.Witness == nil {
		t.Fatalf("status=%v reason=%q, want mismatch", res.Status, res.Reason)
	}
	if res.Witness.Old.Kind != OutcomeError || res.Witness.New.Kind != OutcomeReturn {
		t.Fatalf("want old error/new return, got old=%+v new=%+v",
			res.Witness.Old, res.Witness.New)
	}
}

// TestWidenAdd16To32 exercises the straight-line widening case (no reachable
// error path on byte inputs) where normal returns must remain equal.
func TestWidenAdd16To32(t *testing.T) {
	bo := NewBuilder("a16", 2)
	oldG := bo.Return(bo.Binary(3, OpAdd,
		bo.SExt(1, bo.Input(0), 16),
		bo.SExt(2, bo.Input(1), 16)))

	bn := NewBuilder("a32", 2)
	wide := bn.Binary(3, OpAdd,
		bn.SExt(1, bn.Input(0), 32),
		bn.SExt(2, bn.Input(1), 32))
	newG := bn.Return(bn.Trunc(6, wide, 16))

	res, err := Verify(oldG, newG, Config{Budget: 500_000})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.Status != StatusEquivalent {
		t.Fatalf("status=%v reason=%q witness=%+v", res.Status, res.Reason, res.Witness)
	}
}
