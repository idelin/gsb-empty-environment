package macroexp

import (
	"reflect"
	"testing"
)

func texts(ts []Token) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Text
	}
	return out
}

func pos(line, col int) Pos { return Pos{File: "test.m", Line: line, Col: col} }

func TestProvenanceObjectLike(t *testing.T) {
	src := "#define one() = 1 #end\n" +
		"one()\n"
	res := Expand("test.m", src, 0)
	if len(res.Diags) != 0 {
		t.Fatalf("unexpected diags: %v", res.Diags)
	}
	if got := res.Texts(); !reflect.DeepEqual(got, []string{"1"}) {
		t.Fatalf("tokens = %v", got)
	}
	tok := res.Tokens[0]
	if tok.DefPos != pos(1, 17) {
		t.Errorf("DefPos = %v, want 1:17", tok.DefPos)
	}
	if tok.CallPos != pos(2, 1) {
		t.Errorf("CallPos = %v, want 2:1", tok.CallPos)
	}
	if len(tok.Chain) != 1 {
		t.Fatalf("chain len = %d, want 1", len(tok.Chain))
	}
	f := tok.Chain[0]
	if f.Macro != "one" || f.DefPos != pos(1, 9) || f.CallPos != pos(2, 1) {
		t.Errorf("frame = %+v", f)
	}
}

func TestNestedExpansionChain(t *testing.T) {
	src := "#define inner() = 7 #end\n" +
		"#define outer() = inner() + inner() #end\n" +
		"outer()\n"
	res := Expand("test.m", src, 0)
	if len(res.Diags) != 0 {
		t.Fatalf("unexpected diags: %v", res.Diags)
	}
	if got := res.Texts(); !reflect.DeepEqual(got, []string{"7", "+", "7"}) {
		t.Fatalf("tokens = %v", got)
	}
	first, second := res.Tokens[0], res.Tokens[2]
	for _, tok := range []Token{first, second} {
		if len(tok.Chain) != 2 {
			t.Fatalf("chain len = %d, want 2", len(tok.Chain))
		}
		if tok.Chain[0].Macro != "outer" || tok.Chain[1].Macro != "inner" {
			t.Errorf("chain = %v", FormatChain(tok.Chain))
		}
		if tok.Chain[0].DefPos != pos(2, 9) || tok.Chain[0].CallPos != pos(3, 1) {
			t.Errorf("outer frame = %+v", tok.Chain[0])
		}
		if tok.DefPos != pos(1, 19) {
			t.Errorf("DefPos = %v, want 1:19", tok.DefPos)
		}
	}
	if first.Chain[1].CallPos != pos(2, 19) {
		t.Errorf("first inner call pos = %v, want 2:19", first.Chain[1].CallPos)
	}
	if second.Chain[1].CallPos != pos(2, 29) {
		t.Errorf("second inner call pos = %v, want 2:29", second.Chain[1].CallPos)
	}
}

func TestHygieneSameNameLocal(t *testing.T) {
	// The argument mentions `tmp`, which collides with the macro's local:
	// the local must be renamed, the argument must not be captured.
	src := "#define wrap(x) locals(tmp) = tmp = x ; tmp #end\n" +
		"wrap(tmp)\n"
	res := Expand("test.m", src, 0)
	if len(res.Diags) != 0 {
		t.Fatalf("unexpected diags: %v", res.Diags)
	}
	want := []string{"tmp__1", "=", "tmp", ";", "tmp__1"}
	if got := res.Texts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("tokens = %v, want %v", got, want)
	}
}

func TestHygieneNestedSameMacro(t *testing.T) {
	// Two expansions of the same macro must get distinct local renamings.
	src := "#define wrap(x) locals(tmp) = tmp = x ; tmp #end\n" +
		"wrap(wrap(1))\n"
	res := Expand("test.m", src, 0)
	if len(res.Diags) != 0 {
		t.Fatalf("unexpected diags: %v", res.Diags)
	}
	want := []string{"tmp__2", "=", "tmp__1", "=", "1", ";", "tmp__1", ";", "tmp__2"}
	if got := res.Texts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("tokens = %v, want %v", got, want)
	}
}

func TestEmptyArgument(t *testing.T) {
	src := "#define f(a, b) = [a][b] #end\n" +
		"#define g(x) = <x> #end\n" +
		"#define z() = 9 #end\n" +
		"f(, x)\n" +
		"f(a,)\n" +
		"g()\n" +
		"z()\n"
	res := Expand("test.m", src, 0)
	if len(res.Diags) != 0 {
		t.Fatalf("unexpected diags: %v", res.Diags)
	}
	want := []string{"[", "]", "[", "x", "]", "[", "a", "]", "[", "]", "<", ">", "9"}
	if got := res.Texts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("tokens = %v, want %v", got, want)
	}
}

func TestArgumentExpandedOnceRepeatedReference(t *testing.T) {
	src := "#define one() = 1 #end\n" +
		"#define twice(x) = x + x #end\n" +
		"twice(one())\n"
	res := Expand("test.m", src, 0)
	if len(res.Diags) != 0 {
		t.Fatalf("unexpected diags: %v", res.Diags)
	}
	if got := res.Texts(); !reflect.DeepEqual(got, []string{"1", "+", "1"}) {
		t.Fatalf("tokens = %v", got)
	}
	if res.Counts["one"] != 1 {
		t.Errorf("one expanded %d times, want exactly 1", res.Counts["one"])
	}
	// Both copies share the same provenance: produced by `one`, pulled in
	// through the argument of `twice`.
	for _, tok := range []Token{res.Tokens[0], res.Tokens[2]} {
		if len(tok.Chain) != 2 {
			t.Fatalf("chain len = %d, want 2", len(tok.Chain))
		}
		if tok.Chain[0].Macro != "twice" || !tok.Chain[0].InArg {
			t.Errorf("outer frame = %+v, want twice (in argument)", tok.Chain[0])
		}
		if tok.Chain[1].Macro != "one" || tok.Chain[1].CallPos != pos(3, 7) {
			t.Errorf("inner frame = %+v", tok.Chain[1])
		}
		if tok.DefPos != pos(1, 17) {
			t.Errorf("DefPos = %v, want 1:17", tok.DefPos)
		}
	}
}

func TestErrorInsideArgument(t *testing.T) {
	// The arity error originates inside the actual argument of wrap; the
	// diagnostic must point at the argument text, not at wrap's definition.
	src := "#define pair(a, b) = a b #end\n" +
		"#define wrap(x) = [x] #end\n" +
		"wrap(pair(1))\n"
	res := Expand("test.m", src, 0)
	if len(res.Diags) != 1 {
		t.Fatalf("diags = %v, want exactly 1", res.Diags)
	}
	d := res.Diags[0]
	if d.Kind != DiagArity {
		t.Errorf("kind = %v, want arity error", d.Kind)
	}
	if d.Pos != pos(3, 6) {
		t.Errorf("pos = %v, want 3:6 (the `pair` call inside the argument)", d.Pos)
	}
	if len(d.Chain) != 1 || d.Chain[0].Macro != "wrap" || !d.Chain[0].InArg {
		t.Errorf("chain = %v, want [wrap (in argument)]", FormatChain(d.Chain))
	}
	if d.Chain[0].CallPos != pos(3, 1) {
		t.Errorf("wrap call pos = %v, want 3:1", d.Chain[0].CallPos)
	}
	// The failing call is preserved unexpanded in the output.
	want := []string{"[", "pair", "(", "1", ")", "]"}
	if got := res.Texts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("tokens = %v, want %v", got, want)
	}
}

func TestRecursionSelf(t *testing.T) {
	src := "#define r() = r() #end\n" +
		"r()\n"
	res := Expand("test.m", src, 0)
	if len(res.Diags) != 1 {
		t.Fatalf("diags = %v, want exactly 1", res.Diags)
	}
	d := res.Diags[0]
	if d.Kind != DiagRecursion {
		t.Errorf("kind = %v, want recursion error", d.Kind)
	}
	if d.Pos != pos(1, 15) {
		t.Errorf("pos = %v, want 1:15 (re-entrant call in the body)", d.Pos)
	}
	if len(d.Chain) != 1 || d.Chain[0].Macro != "r" {
		t.Errorf("chain = %v, want shortest evidence [r]", FormatChain(d.Chain))
	}
	if got := res.Texts(); !reflect.DeepEqual(got, []string{"r", "(", ")"}) {
		t.Fatalf("tokens = %v, want unexpanded call preserved", got)
	}
}

func TestRecursionMutual(t *testing.T) {
	src := "#define a() = b() #end\n" +
		"#define b() = a() #end\n" +
		"a()\n"
	res := Expand("test.m", src, 0)
	if len(res.Diags) != 1 {
		t.Fatalf("diags = %v, want exactly 1", res.Diags)
	}
	d := res.Diags[0]
	if d.Kind != DiagRecursion {
		t.Errorf("kind = %v, want recursion error", d.Kind)
	}
	if d.Pos != pos(2, 15) {
		t.Errorf("pos = %v, want 2:15 (the `a` call inside b's body)", d.Pos)
	}
	// Shortest evidence of the cycle: a -> b.
	if len(d.Chain) != 2 || d.Chain[0].Macro != "a" || d.Chain[1].Macro != "b" {
		t.Errorf("chain = %v, want [a -> b]", FormatChain(d.Chain))
	}
	if d.Chain[0].CallPos != pos(3, 1) || d.Chain[1].CallPos != pos(1, 15) {
		t.Errorf("chain call positions = %+v", d.Chain)
	}
	if got := res.Texts(); !reflect.DeepEqual(got, []string{"a", "(", ")"}) {
		t.Fatalf("tokens = %v, want unexpanded call preserved", got)
	}
}

func TestBudgetExhausted(t *testing.T) {
	src := "#define d(x) = x x #end\n" +
		"#define start() = d(d(d(1))) #end\n" +
		"start()\n"
	res := Expand("test.m", src, 3)
	if len(res.Diags) != 1 {
		t.Fatalf("diags = %v, want exactly 1 (reported once)", res.Diags)
	}
	d := res.Diags[0]
	if d.Kind != DiagBudget {
		t.Errorf("kind = %v, want budget error", d.Kind)
	}
	if d.Pos != pos(2, 23) {
		t.Errorf("pos = %v, want 2:23 (innermost d, where the budget ran out)", d.Pos)
	}
	// Shortest evidence at exhaustion: start, then two nested argument
	// expansions of d.
	if len(d.Chain) != 3 || d.Chain[0].Macro != "start" ||
		d.Chain[1].Macro != "d" || !d.Chain[1].InArg ||
		d.Chain[2].Macro != "d" || !d.Chain[2].InArg {
		t.Errorf("chain = %v, want [start -> d (in argument) -> d (in argument)]",
			FormatChain(d.Chain))
	}
	if res.Counts["d"] != 2 {
		t.Errorf("d expanded %d times, want 2 (budget of 3, one used by start)", res.Counts["d"])
	}
	// The innermost call is preserved unexpanded as evidence.
	want := []string{"d", "(", "1", ")", "d", "(", "1", ")", "d", "(", "1", ")", "d", "(", "1", ")"}
	if got := res.Texts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("tokens = %v, want %v", got, want)
	}
}

func TestNestedCallInArgumentIsNotRecursion(t *testing.T) {
	// d(d(1)) nests calls but terminates; it must not be flagged.
	src := "#define d(x) = x + x #end\n" +
		"d(d(1))\n"
	res := Expand("test.m", src, 0)
	if len(res.Diags) != 0 {
		t.Fatalf("unexpected diags: %v", res.Diags)
	}
	if got := res.Texts(); !reflect.DeepEqual(got, []string{"1", "+", "1", "+", "1", "+", "1"}) {
		t.Fatalf("tokens = %v", got)
	}
	if res.Counts["d"] != 2 {
		t.Errorf("d expanded %d times, want 2", res.Counts["d"])
	}
}
