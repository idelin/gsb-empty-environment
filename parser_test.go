package errmerge

import (
	"reflect"
	"testing"
)

// Ambiguous-prefix grammar used across tests:
//   S → Alt(Seq("a","b","c","d"), Seq("a","b","c")) then "e"
// Both alternatives share the prefix "abc".
func ambiguousS() Rule {
	return Named("S", Seq(
		Alt(
			Seq(Tok("a"), Tok("b"), Tok("c"), Tok("d")),
			Seq(Tok("a"), Tok("b"), Tok("c")),
		),
		Tok("e"),
	))
}

func expectDiag(t *testing.T, d *Diag, pos int, expected ...string) {
	t.Helper()
	if d == nil {
		t.Fatalf("expected failure diag, got success")
	}
	if d.Pos != pos {
		t.Errorf("Pos = %d, want %d", d.Pos, pos)
	}
	got := d.SortedExpected()
	want := append([]string(nil), expected...)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Expected = %v, want %v", got, want)
	}
}

// A backtracked branch's successful consumption ("abc" in the abandoned
// first alternative) must not leak into the final diagnostic: only the
// live path's failure at "e" is reported.
func TestBacktrackDiscardsAbandonedConsumption(t *testing.T) {
	p := NewParser("abcf", 0)
	_, d := ambiguousS()(p)
	expectDiag(t, d, 3, "'e'")
	if p.Pos != 0 {
		t.Errorf("parser Pos = %d after failed parse, want 0 (full backtrack)", p.Pos)
	}
}

// The same grammar succeeds via the second alternative after the first
// consumes "abc" and fails on "d": success must produce no diagnostic.
func TestBacktrackThenSucceed(t *testing.T) {
	p := NewParser("abce", 0)
	v, d := ambiguousS()(p)
	if d != nil {
		t.Fatalf("expected success, got %v", d)
	}
	if v == nil || p.Pos != len(p.Src) {
		t.Errorf("success did not consume full input, Pos = %d", p.Pos)
	}
}

// Both alternatives fail at the same position: expected sets merge.
func TestMergeExpectedAtSamePosition(t *testing.T) {
	s := Named("S", Alt(
		Seq(Tok("a"), Tok("b")),
		Seq(Tok("a"), Tok("c")),
	))
	p := NewParser("ax", 0)
	_, d := s(p)
	expectDiag(t, d, 1, "'b'", "'c'")
}

// The farthest failure wins over a nearer one from another branch.
func TestFarthestPositionWins(t *testing.T) {
	s := Named("S", Alt(
		Seq(Tok("a"), Tok("b"), Tok("c")),
		Seq(Tok("a"), Tok("z")),
	))
	p := NewParser("abx", 0)
	_, d := s(p)
	expectDiag(t, d, 2, "'c'")
}

// Failures at end of input are anchored exactly at len(src).
func TestEOFPosition(t *testing.T) {
	p := NewParser("a", 0)
	_, d := Seq(Tok("a"), Tok("b"))(p)
	expectDiag(t, d, 1, "'b'")
	if d.Pos != len(p.Src) {
		t.Errorf("EOF failure Pos = %d, want len(src) = %d", d.Pos, len(p.Src))
	}
}

// The EOF terminal reports leftover input.
func TestEOFTerminal(t *testing.T) {
	p := NewParser("ab", 0)
	_, d := Seq(Tok("a"), EOF())(p)
	expectDiag(t, d, 1, "<EOF>")
}

// Recursive grammar hitting the depth limit reports a depth-limit
// diagnostic at the position where the limit tripped.
func TestRecursionDepthLimit(t *testing.T) {
	var expr Rule
	expr = func(p *Parser) (interface{}, *Diag) {
		return Named("expr", Alt(
			Seq(Tok("("), expr, Tok(")")),
			Tok("x"),
		))(p)
	}
	p := NewParser("((((x", 3)
	_, d := expr(p)
	expectDiag(t, d, 3, "depth-limit:expr")
}

// Without a depth limit the same recursive grammar parses fine.
func TestRecursionWithinLimit(t *testing.T) {
	var expr Rule
	expr = func(p *Parser) (interface{}, *Diag) {
		return Named("expr", Alt(
			Seq(Tok("("), expr, Tok(")")),
			Tok("x"),
		))(p)
	}
	p := NewParser("(((x)))", 0)
	_, d := Seq(expr, EOF())(p)
	if d != nil {
		t.Fatalf("expected success, got %v", d)
	}
}

// Same context reached via a short and a long path: the merged diagnostic
// keeps the shortest source chain for that context.
func TestShortestChainPerContext(t *testing.T) {
	x := Named("X", Tok("q"))
	long := Named("Inner", Named("Mid", x))
	s := Named("S", Alt(
		Seq(Tok("a"), x),
		Seq(Tok("a"), long),
	))
	p := NewParser("a!", 0)
	_, d := s(p)
	expectDiag(t, d, 1, "'q'")
	chain, ok := d.Chains["X"]
	if !ok {
		t.Fatalf("no chain recorded for context X, chains: %v", d.Chains)
	}
	var names []string
	for _, f := range chain {
		names = append(names, f.Rule)
	}
	if !reflect.DeepEqual(names, []string{"S", "X"}) {
		t.Errorf("chain for X = %v, want shortest [S X]", names)
	}
}

// Failures from distinct calling contexts at the same position each keep
// their own (shortest) chain.
func TestDistinctContextsPreserved(t *testing.T) {
	s := Named("S", Alt(
		Seq(Tok("a"), Named("X", Tok("q"))),
		Seq(Tok("a"), Named("Y", Tok("q"))),
	))
	p := NewParser("a!", 0)
	_, d := s(p)
	expectDiag(t, d, 1, "'q'")
	for _, ctx := range []string{"X", "Y"} {
		chain, ok := d.Chains[ctx]
		if !ok {
			t.Fatalf("missing chain for context %s, chains: %v", ctx, d.Chains)
		}
		if len(chain) != 2 || chain[0].Rule != "S" || chain[1].Rule != ctx {
			t.Errorf("chain for %s = %v, want [S %s]", ctx, chain, ctx)
		}
	}
}

// Merge unit test: farther position wins regardless of argument order.
func TestMergeFarthestWins(t *testing.T) {
	near := New(1, "'a'")
	far := New(5, "'b'")
	if Merge(near, far) != far || Merge(far, near) != far {
		t.Errorf("Merge should return the farther diagnostic")
	}
	if Merge(nil, far) != far || Merge(far, nil) != far {
		t.Errorf("Merge should be nil-safe")
	}
}
