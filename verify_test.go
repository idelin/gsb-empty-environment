package netsec

import (
	"testing"
)

func checkedMulGraph(width, checkWidth uint8, errorSource NodeID) (*Graph, NodeID) {
	b := NewBuilder(1)
	x := b.Input(0)
	sx := b.Extend(x, width, true)
	k := b.Const(width, 300)
	mul := b.MulChecked(sx, k, width, checkWidth, true)
	ov := b.Overflow(mul)
	ret := b.Return(mul)
	err := b.Error(errorSource)
	entry := b.Branch(ov, err, ret)
	return b.MustBuild(entry), mul
}

func checkedBinaryGraph(op Op, width, checkWidth uint8, constant int64, errorSource NodeID) (*Graph, NodeID) {
	b := NewBuilder(1)
	x := b.Input(0)
	sx := b.Extend(x, width, true)
	k := b.Const(width, uint64(constant))
	var arith NodeID
	switch op {
	case OpAdd:
		arith = b.AddChecked(sx, k, width, checkWidth, true)
	case OpSub:
		arith = b.SubChecked(sx, k, width, checkWidth, true)
	case OpMul:
		arith = b.MulChecked(sx, k, width, checkWidth, true)
	default:
		panic("unsupported test op")
	}
	ov := b.Overflow(arith)
	ret := b.Return(arith)
	err := b.Error(errorSource)
	entry := b.Branch(ov, err, ret)
	return b.MustBuild(entry), arith
}

func thresholdGraph(width uint8, threshold int64) *Graph {
	b := NewBuilder(1)
	x := b.Input(0)
	sx := b.Extend(x, width, true)
	k := b.Const(width, uint64(threshold))
	neg := b.Cmp(sx, k, CmpLt, true)
	ret := b.Return(sx)
	err := b.Error(100)
	entry := b.Branch(neg, err, ret)
	return b.MustBuild(entry)
}

func TestWidenedCheckedAddAndSubEquivalent(t *testing.T) {
	for _, op := range []Op{OpAdd, OpSub} {
		t.Run(op.String(), func(t *testing.T) {
			old, source := checkedBinaryGraph(op, 16, 16, 32767, 0)
			old, _ = checkedBinaryGraph(op, 16, 16, 32767, source)
			new, _ := checkedBinaryGraph(op, 32, 16, 32767, source)

			result := Verify(old, new)
			if result.Status != StatusVerified {
				t.Fatalf("Verify status = %s, reason = %q", result.Status, result.Reason)
			}
			for i := 0; i < 256; i++ {
				input := []byte{byte(i)}
				o, err := Interpret(old, input)
				if err != nil {
					t.Fatal(err)
				}
				n, err := Interpret(new, input)
				if err != nil {
					t.Fatal(err)
				}
				if !o.Equal(n) {
					t.Fatalf("input %d differs: old=%v new=%v", i, o, n)
				}
			}
		})
	}
}

func TestWidenedCheckedMultiplicationEquivalent(t *testing.T) {
	old, source := checkedMulGraph(16, 16, 0)
	// The error source is a provenance ID shared by both versions.
	old, source = checkedMulGraph(16, 16, source)
	new, _ := checkedMulGraph(32, 16, source)

	result := Verify(old, new)
	if result.Status != StatusVerified {
		t.Fatalf("Verify status = %s, reason = %q", result.Status, result.Reason)
	}
	if result.PathsOld != 2 || result.PathsNew != 2 {
		t.Fatalf("path counts = %d/%d, want 2/2", result.PathsOld, result.PathsNew)
	}

	for i := 0; i < 256; i++ {
		input := []byte{byte(i)}
		o, err := Interpret(old, input)
		if err != nil {
			t.Fatal(err)
		}
		n, err := Interpret(new, input)
		if err != nil {
			t.Fatal(err)
		}
		if !o.Equal(n) {
			t.Fatalf("input %d differs: old=%v new=%v", i, o, n)
		}
	}
}

func TestWrongErrorSourceHasCounterexample(t *testing.T) {
	old, source := checkedMulGraph(16, 16, 0)
	old, source = checkedMulGraph(16, 16, source)
	new, _ := checkedMulGraph(32, 16, source+1000)

	result := Verify(old, new)
	if result.Status != StatusCounterexample {
		t.Fatalf("Verify status = %s, reason = %q", result.Status, result.Reason)
	}
	cx := result.Counterexample
	if cx == nil {
		t.Fatal("missing counterexample")
	}
	o, err := Interpret(old, cx.Input)
	if err != nil {
		t.Fatal(err)
	}
	n, err := Interpret(new, cx.Input)
	if err != nil {
		t.Fatal(err)
	}
	if o.Equal(n) {
		t.Fatalf("replayed counterexample is equivalent: old=%v new=%v", o, n)
	}
	if !o.IsError() || !n.IsError() {
		t.Fatalf("expected two errors, got old=%v new=%v", o, n)
	}
}

func TestWidenedCheckStillUsesSourceWidth(t *testing.T) {
	old, source := checkedMulGraph(16, 16, 0)
	old, source = checkedMulGraph(16, 16, source)
	new, _ := checkedMulGraph(32, 17, source)

	result := Verify(old, new)
	if result.Status != StatusCounterexample {
		t.Fatalf("Verify status = %s, reason = %q", result.Status, result.Reason)
	}
	cx := result.Counterexample
	o, err := Interpret(old, cx.Input)
	if err != nil {
		t.Fatal(err)
	}
	n, err := Interpret(new, cx.Input)
	if err != nil {
		t.Fatal(err)
	}
	if !o.IsError() || n.IsError() {
		t.Fatalf("counterexample must be old error / new return, got old=%v new=%v", o, n)
	}
}

func TestBranchTruncExtEquivalenceExhaustive(t *testing.T) {
	build := func(width uint8) *Graph {
		b := NewBuilder(1)
		x := b.Input(0)
		sx := b.Extend(x, width, true)
		zero := b.Const(width, 0)
		neg := b.Cmp(sx, zero, CmpLt, true)
		t8 := b.Trunc(sx, 8)
		sx2 := b.Extend(t8, width, true)
		ret := b.Return(sx2)
		err := b.Error(42)
		entry := b.Branch(neg, err, ret)
		return b.MustBuild(entry)
	}
	old := build(16)
	new := build(32)

	result := Verify(old, new)
	if result.Status != StatusVerified {
		t.Fatalf("Verify status = %s, reason = %q", result.Status, result.Reason)
	}
	for i := 0; i < 256; i++ {
		input := []byte{byte(i)}
		o, err := Interpret(old, input)
		if err != nil {
			t.Fatal(err)
		}
		n, err := Interpret(new, input)
		if err != nil {
			t.Fatal(err)
		}
		if !o.Equal(n) {
			t.Fatalf("input %d differs: old=%v new=%v", i, o, n)
		}
	}
}

func TestSymbolicCounterexampleMatchesExhaustiveEightBitGraph(t *testing.T) {
	old := thresholdGraph(16, 0)
	new := thresholdGraph(32, -64)

	result := Verify(old, new)
	if result.Status != StatusCounterexample {
		t.Fatalf("Verify status = %s, reason = %q", result.Status, result.Reason)
	}
	if result.Counterexample == nil {
		t.Fatal("missing counterexample")
	}

	mismatches := make(map[byte]bool)
	for i := 0; i < 256; i++ {
		input := []byte{byte(i)}
		o, err := Interpret(old, input)
		if err != nil {
			t.Fatal(err)
		}
		n, err := Interpret(new, input)
		if err != nil {
			t.Fatal(err)
		}
		if !o.Equal(n) {
			mismatches[byte(i)] = true
		}
	}
	if len(mismatches) != 64 {
		t.Fatalf("exhaustive mismatches = %d, want 64", len(mismatches))
	}
	got := result.Counterexample.Input[0]
	if !mismatches[got] {
		t.Fatalf("symbolic witness %d is not an exhaustive mismatch", got)
	}
}

func TestBudgetExhaustionIsUnknown(t *testing.T) {
	old := thresholdGraph(16, 0)
	new := thresholdGraph(32, 0)
	result := Verify(old, new, Budget{MaxPaths: 1})
	if result.Status != StatusUnknown {
		t.Fatalf("Verify status = %s, want unknown (reason %q)", result.Status, result.Reason)
	}
	if result.Counterexample != nil {
		t.Fatal("unknown result must not carry a counterexample")
	}
}

func TestNormalReturnMismatch(t *testing.T) {
	build := func(width uint8) *Graph {
		b := NewBuilder(1)
		x := b.Input(0)
		sx := b.Extend(x, width, true)
		k := b.Const(width, 300)
		mul := b.Mul(sx, k, width)
		ret := b.Return(mul)
		return b.MustBuild(ret)
	}
	old := build(16)
	new := build(32)
	result := Verify(old, new)
	if result.Status != StatusCounterexample {
		t.Fatalf("Verify status = %s, reason = %q", result.Status, result.Reason)
	}
	cx := result.Counterexample
	o, err := Interpret(old, cx.Input)
	if err != nil {
		t.Fatal(err)
	}
	n, err := Interpret(new, cx.Input)
	if err != nil {
		t.Fatal(err)
	}
	if o.IsError() || n.IsError() || o.Value == n.Value {
		t.Fatalf("expected differing normal returns, got old=%v new=%v", o, n)
	}
}

func TestSATBudgetExhaustionIsUnknown(t *testing.T) {
	old := thresholdGraph(16, 0)
	new := thresholdGraph(32, -64)
	result := Verify(old, new, Budget{MaxSATNodes: 1})
	if result.Status != StatusUnknown {
		t.Fatalf("Verify status = %s, want unknown (reason %q)", result.Status, result.Reason)
	}
}

func TestInvalidGraphRejected(t *testing.T) {
	b := NewBuilder(1)
	x := b.Input(0)
	if _, err := b.Build(x); err == nil { // an input is not a control node
		t.Fatal("Validate accepted a value node as entry")
	}
}

func TestTwelveInputBytes(t *testing.T) {
	build := func() *Graph {
		b := NewBuilder(12)
		acc := b.Const(16, 0)
		for i := 0; i < 12; i++ {
			x := b.Input(i)
			sx := b.Extend(x, 16, true)
			acc = b.Add(acc, sx, 16)
		}
		ret := b.Return(acc)
		return b.MustBuild(ret)
	}
	old := build()
	new := build()
	result := Verify(old, new)
	if result.Status != StatusVerified {
		t.Fatalf("Verify status = %s, reason = %q", result.Status, result.Reason)
	}
}
