package netsec

import "testing"

// buildTruncationGraph models the classic bug: a 32-bit length computed
// from an 8-bit field is truncated to 8 bits for the allocation, while
// the full 32-bit value drives the write.
//
//	x: u8 input
//	n = zext32(x) * 32
//	alloc(trunc8(n))
//	write(n)
func buildTruncationGraph(t *testing.T) *Graph {
	t.Helper()
	b := NewBuilder()
	x := b.Input(0, W8)
	n := b.Mul(b.Ext(x, W32), b.Const(W32, 32))
	b.Alloc(b.Trunc(n, W8))
	b.Write(n)
	g, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g
}

// groundTruth decides the verdict by exhaustively executing every input
// (feasible only for the small widths used in tests).
func groundTruth(t *testing.T, g *Graph) Verdict {
	t.Helper()
	n := g.NumInputBytes()
	if n > 3 {
		t.Fatalf("ground truth too expensive for %d input bytes", n)
	}
	total := 1 << (8 * uint(n))
	for i := 0; i < total; i++ {
		var in [MaxInputs]byte
		v := i
		for j := 0; j < n; j++ {
			in[j] = byte(v)
			v >>= 8
		}
		ok, err := Violates(g, in)
		if err != nil {
			t.Fatalf("Violates(%v): %v", in, err)
		}
		if ok {
			return Vulnerable
		}
	}
	return Safe
}

func TestAnalyzeVulnerableTruncation(t *testing.T) {
	g := buildTruncationGraph(t)
	res := Analyze(g, Options{})
	if res.Verdict != Vulnerable {
		t.Fatalf("verdict = %v, want vulnerable", res.Verdict)
	}
	// x=8 is the smallest triggering input: 8*32=256 truncates to 0.
	if res.Witness[0] != 8 {
		t.Fatalf("witness = %v, want input byte 0 == 8", res.Witness)
	}
	ok, err := Violates(g, res.Witness)
	if err != nil || !ok {
		t.Fatalf("witness does not trigger a violation: ok=%v err=%v", ok, err)
	}
	if groundTruth(t, g) != Vulnerable {
		t.Fatal("exhaustive check disagrees")
	}
}

// buildGuardedGraph is safe: on every path the allocation covers the
// write.
//
//	if x < 200 { alloc(zext16(x)+56); write(zext16(x)) }
//	else       { alloc(300); write(10) }
func buildGuardedGraph(t *testing.T) *Graph {
	t.Helper()
	b := NewBuilder()
	x := b.Input(0, W8)
	cond := b.Cmp(LT, x, b.Const(W8, 200))
	then, els := b.NewBlock(), b.NewBlock()
	b.Branch(cond, then, els)
	b.Switch(then)
	x16 := b.Ext(x, W16)
	b.Alloc(b.Add(x16, b.Const(W16, 56)))
	b.Write(x16)
	b.Switch(els)
	b.Alloc(b.Const(W16, 300))
	b.Write(b.Const(W16, 10))
	g, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g
}

func TestAnalyzeSafeGuarded(t *testing.T) {
	g := buildGuardedGraph(t)
	res := Analyze(g, Options{})
	if res.Verdict != Safe {
		t.Fatalf("verdict = %v, want safe", res.Verdict)
	}
	if res.Paths != 2 {
		t.Fatalf("paths = %d, want 2", res.Paths)
	}
	if groundTruth(t, g) != Safe {
		t.Fatal("exhaustive check disagrees")
	}
}

// buildElseBugGraph hides the bug on the else branch.
//
//	if x < 10 { alloc(300); write(zext16(x)) }
//	else      { n = zext16(x)*2; alloc(trunc8(n)); write(n) }
func buildElseBugGraph(t *testing.T) *Graph {
	t.Helper()
	b := NewBuilder()
	x := b.Input(0, W8)
	cond := b.Cmp(LT, x, b.Const(W8, 10))
	then, els := b.NewBlock(), b.NewBlock()
	b.Branch(cond, then, els)
	b.Switch(then)
	b.Alloc(b.Const(W16, 300))
	b.Write(b.Ext(x, W16))
	b.Switch(els)
	n := b.Mul(b.Ext(x, W16), b.Const(W16, 2))
	b.Alloc(b.Trunc(n, W8))
	b.Write(n)
	g, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g
}

func TestAnalyzeVulnerableElseBranch(t *testing.T) {
	g := buildElseBugGraph(t)
	res := Analyze(g, Options{})
	if res.Verdict != Vulnerable {
		t.Fatalf("verdict = %v, want vulnerable", res.Verdict)
	}
	// x=128 is the smallest triggering input: 2*128=256 truncates to 0.
	if res.Witness[0] != 128 {
		t.Fatalf("witness = %v, want input byte 0 == 128", res.Witness)
	}
	if ok, _ := Violates(g, res.Witness); !ok {
		t.Fatal("witness does not trigger a violation")
	}
	if groundTruth(t, g) != Vulnerable {
		t.Fatal("exhaustive check disagrees")
	}
}

// buildInfeasibleGraph has an overflowing write on an infeasible path
// (x < 10 && x > 20); the program is safe.
func buildInfeasibleGraph(t *testing.T) *Graph {
	t.Helper()
	b := NewBuilder()
	x := b.Input(0, W8)
	c1 := b.Cmp(LT, x, b.Const(W8, 10))
	b1, b2 := b.NewBlock(), b.NewBlock()
	b.Branch(c1, b1, b2)
	b.Switch(b1)
	c2 := b.Cmp(GT, x, b.Const(W8, 20))
	b3, b4 := b.NewBlock(), b.NewBlock()
	b.Branch(c2, b3, b4)
	b.Switch(b3)
	b.Alloc(b.Const(W8, 1))
	b.Write(b.Const(W8, 200))
	b.Switch(b4)
	b.Alloc(b.Const(W8, 10))
	b.Write(b.Const(W8, 5))
	b.Switch(b2)
	b.Alloc(b.Const(W8, 10))
	b.Write(b.Const(W8, 5))
	g, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g
}

func TestAnalyzeInfeasiblePathSafe(t *testing.T) {
	g := buildInfeasibleGraph(t)
	res := Analyze(g, Options{})
	if res.Verdict != Safe {
		t.Fatalf("verdict = %v, want safe", res.Verdict)
	}
	if groundTruth(t, g) != Safe {
		t.Fatal("exhaustive check disagrees")
	}
}

// buildTautologyGraph allocs and writes the same value: safe, but only
// provable by enumeration, not by intervals.
func buildTautologyGraph(t *testing.T) *Graph {
	t.Helper()
	b := NewBuilder()
	x := b.Input(0, W8)
	b.Alloc(x)
	b.Write(x)
	g, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g
}

func TestAnalyzeBudgetExhaustedUnknown(t *testing.T) {
	g := buildTautologyGraph(t)
	// One budget unit is consumed by path exploration; nothing remains
	// for the 256 evaluations needed to prove safety.
	res := Analyze(g, Options{Budget: 1})
	if res.Verdict != Unknown {
		t.Fatalf("verdict = %v, want unknown", res.Verdict)
	}
	// With enough budget the same graph is proven safe.
	res = Analyze(g, Options{})
	if res.Verdict != Safe {
		t.Fatalf("verdict = %v, want safe", res.Verdict)
	}
	if groundTruth(t, g) != Safe {
		t.Fatal("exhaustive check disagrees")
	}
}

func TestAnalyzeVulnerableNeedsBudget(t *testing.T) {
	g := buildTruncationGraph(t)
	res := Analyze(g, Options{Budget: 1})
	if res.Verdict != Unknown {
		t.Fatalf("verdict = %v, want unknown", res.Verdict)
	}
	res = Analyze(g, Options{Budget: 16})
	if res.Verdict != Vulnerable {
		t.Fatalf("verdict = %v, want vulnerable", res.Verdict)
	}
}

// buildWideSafeGraph reads all 8 input bytes; safety follows from the
// interval proof alone (write <= 255 == alloc), so even a tiny budget
// suffices.
func buildWideSafeGraph(t *testing.T) *Graph {
	t.Helper()
	b := NewBuilder()
	acc := b.Input(0, W8)
	for i := 1; i < MaxInputs; i++ {
		acc = b.Add(acc, b.Input(i, W8))
	}
	b.Alloc(b.Const(W8, 255))
	b.Write(acc)
	g, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g
}

func TestAnalyzeIntervalProofSafe(t *testing.T) {
	g := buildWideSafeGraph(t)
	if g.NumInputBytes() != MaxInputs {
		t.Fatalf("inputs = %d, want %d", g.NumInputBytes(), MaxInputs)
	}
	res := Analyze(g, Options{Budget: 1})
	if res.Verdict != Safe {
		t.Fatalf("verdict = %v, want safe", res.Verdict)
	}
}

// TestExhaustiveCrossCheck compares Analyze against brute-force execution
// of every input for a table of small-width graphs.
func TestExhaustiveCrossCheck(t *testing.T) {
	cases := map[string]func(*testing.T) *Graph{
		"truncation":  buildTruncationGraph,
		"guarded":     buildGuardedGraph,
		"elseBug":     buildElseBugGraph,
		"infeasible":  buildInfeasibleGraph,
		"tautology":   buildTautologyGraph,
		"twoByteMul":  buildTwoByteMulGraph,
		"constUnsafe": buildConstUnsafeGraph,
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			g := build(t)
			want := groundTruth(t, g)
			res := Analyze(g, Options{})
			if res.Verdict != want {
				t.Fatalf("verdict = %v, exhaustive check says %v", res.Verdict, want)
			}
			if res.Verdict == Vulnerable {
				ok, err := Violates(g, res.Witness)
				if err != nil || !ok {
					t.Fatalf("witness %v does not violate: ok=%v err=%v",
						res.Witness, ok, err)
				}
			}
		})
	}
}

// buildTwoByteMulGraph overflows when the product of two input bytes
// exceeds 255 in the low 8 bits used for the allocation.
func buildTwoByteMulGraph(t *testing.T) *Graph {
	t.Helper()
	b := NewBuilder()
	x := b.Input(0, W8)
	y := b.Input(1, W8)
	n := b.Mul(b.Ext(x, W16), b.Ext(y, W16))
	b.Alloc(b.Trunc(n, W8))
	b.Write(n)
	g, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g
}

// buildConstUnsafeGraph writes more than it allocates on every input.
func buildConstUnsafeGraph(t *testing.T) *Graph {
	t.Helper()
	b := NewBuilder()
	x := b.Input(0, W8)
	cond := b.Cmp(GE, x, b.Const(W8, 0)) // always true
	then, els := b.NewBlock(), b.NewBlock()
	b.Branch(cond, then, els)
	b.Switch(then)
	b.Alloc(b.Const(W8, 4))
	b.Write(b.Const(W8, 8))
	b.Switch(els)
	b.Alloc(b.Const(W8, 16))
	b.Write(b.Const(W8, 1))
	g, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g
}

func TestExecuteEvents(t *testing.T) {
	g := buildTruncationGraph(t)
	var in [MaxInputs]byte
	in[0] = 8
	events, err := Execute(g, in)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %v, want 2", events)
	}
	if events[0].Kind != EventAlloc || events[0].Value != 0 {
		t.Fatalf("alloc event = %+v, want 0 bytes", events[0])
	}
	if events[1].Kind != EventWrite || events[1].Value != 256 {
		t.Fatalf("write event = %+v, want 256 bytes", events[1])
	}
}

func TestBuilderValidation(t *testing.T) {
	t.Run("widthMismatch", func(t *testing.T) {
		b := NewBuilder()
		x := b.Input(0, W8)
		y := b.Const(W16, 1)
		b.Add(x, y)
		if _, err := b.Build(); err != ErrWidth {
			t.Fatalf("err = %v, want ErrWidth", err)
		}
	})
	t.Run("inputRange", func(t *testing.T) {
		b := NewBuilder()
		b.Input(7, W16)
		if _, err := b.Build(); err != ErrInput {
			t.Fatalf("err = %v, want ErrInput", err)
		}
	})
	t.Run("cycle", func(t *testing.T) {
		b := NewBuilder()
		x := b.Input(0, W8)
		cond := b.Cmp(EQ, x, b.Const(W8, 0))
		blk := b.NewBlock()
		b.Branch(cond, blk, blk)
		b.Switch(blk)
		b.Fallthrough(0) // back edge
		if _, err := b.Build(); err != ErrCycle {
			t.Fatalf("err = %v, want ErrCycle", err)
		}
	})
	t.Run("dominance", func(t *testing.T) {
		b := NewBuilder()
		x := b.Input(0, W8)
		cond := b.Cmp(EQ, x, b.Const(W8, 0))
		then, els := b.NewBlock(), b.NewBlock()
		join := b.NewBlock()
		b.Branch(cond, then, els)
		b.Switch(then)
		y := b.Add(x, b.Const(W8, 1))
		b.Fallthrough(join)
		b.Switch(els)
		b.Fallthrough(join)
		b.Switch(join)
		b.Alloc(y) // y defined only on the then path
		if _, err := b.Build(); err != ErrDominance {
			t.Fatalf("err = %v, want ErrDominance", err)
		}
	})
}
