package macroexp

import (
	"fmt"
	"strings"
	"testing"
)

func expandOK(t *testing.T, src string) Result {
	t.Helper()
	res, errs := New(Options{}).Expand("test", src)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	return res
}

func TestBasicParams(t *testing.T) {
	res := expandOK(t, "#def ADD(a, b) a + b\nADD(1, 2)\n")
	if res.Text != "1 + 2\n" {
		t.Fatalf("got %q", res.Text)
	}
}

func TestNestedCalls(t *testing.T) {
	src := `#def ID(x) x
#def ADD(a, b) a + b
#def INC(x) ADD(x, 1)
INC(ID(41)) + INC(1)
`
	res := expandOK(t, src)
	if res.Text != "41 + 1 + 1 + 1\n" {
		t.Fatalf("got %q", res.Text)
	}
}

func TestUnknownMacroPassthrough(t *testing.T) {
	res := expandOK(t, "hello WORLD(1, 2)\n")
	if res.Text != "hello WORLD(1, 2)\n" {
		t.Fatalf("got %q", res.Text)
	}
}

// Same-name locals in different expansions (and nested expansions) must
// get distinct hygienic names, and must not capture argument identifiers.
func TestHygieneSameNameLocals(t *testing.T) {
	src := `#def SET(x)
#local tmp = x
tmp
#end
SET(a)
SET(b)
`
	res := expandOK(t, src)
	if got := strings.Count(res.Text, "tmp#1"); got != 2 {
		t.Fatalf("first expansion: want 2 uses of tmp#1, got %d in %q", got, res.Text)
	}
	if got := strings.Count(res.Text, "tmp#2"); got != 2 {
		t.Fatalf("second expansion: want 2 uses of tmp#2, got %d in %q", got, res.Text)
	}
}

func TestHygieneNoCapture(t *testing.T) {
	src := `#def F(x) #local tmp = x ; tmp
F(tmp)
`
	res := expandOK(t, src)
	// The local is renamed to tmp#1; the argument's own tmp is untouched.
	if res.Text != "tmp#1 = tmp ; tmp#1\n" {
		t.Fatalf("got %q", res.Text)
	}
}

func TestHygieneNestedMacros(t *testing.T) {
	src := `#def INNER(x)
#local acc = x
acc
#end
#def OUTER(x)
#local acc = x
INNER(acc)
#end
OUTER(v)
`
	res := expandOK(t, src)
	if !strings.Contains(res.Text, "acc#1") || !strings.Contains(res.Text, "acc#2") {
		t.Fatalf("want distinct fresh names acc#1 and acc#2, got %q", res.Text)
	}
	if !strings.Contains(res.Text, "acc#2 = acc#1") {
		t.Fatalf("inner local bound to outer renamed local, got %q", res.Text)
	}
}

func TestEmptyArgs(t *testing.T) {
	src := `#def F(x) [x]
#def G(a, b) a|b
#def Z 9
F()
G(,)
Z()
`
	res := expandOK(t, src)
	if res.Text != "[]\n|\n9\n" {
		t.Fatalf("got %q", res.Text)
	}
}

// A parameter referenced twice must not cause its argument to be
// expanded twice.
func TestArgExpandedOnce(t *testing.T) {
	src := `#def LEAF() big
#def D(x) x x
D(D(D(LEAF())))
`
	exp := New(Options{})
	res, errs := exp.Expand("test", src)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if got := strings.Count(res.Text, "big"); got != 8 {
		t.Fatalf("want 8 copies of big, got %d in %q", got, res.Text)
	}
	// 3 D expansions + 1 LEAF expansion; without memoization LEAF would
	// be expanded 8 times and the D's re-expanded at every level.
	if exp.Steps() != 4 {
		t.Fatalf("want 4 expansion steps, got %d", exp.Steps())
	}
}

func TestRepeatedReferencesShareProvenance(t *testing.T) {
	src := `#def D(x) x x
D(hi)
`
	res := expandOK(t, src)
	var his []OutToken
	for _, tok := range res.Tokens {
		if tok.Text == "hi" {
			his = append(his, tok)
		}
	}
	if len(his) != 2 {
		t.Fatalf("want 2 hi tokens, got %d", len(his))
	}
	for _, h := range his {
		if h.Def.Line != 2 || h.Def.Col != 3 {
			t.Fatalf("arg token def = %v, want 2:3", h.Def)
		}
		if len(h.Chain) != 1 || h.Chain[0].Macro != "D" {
			t.Fatalf("arg token chain = %+v", h.Chain)
		}
	}
}

func TestProvenance(t *testing.T) {
	src := `#def WRAP(x) <x>
WRAP(hi)
`
	res := expandOK(t, src)
	// Tokens: "<", "hi", ">", "\n"
	if len(res.Tokens) != 4 {
		t.Fatalf("want 4 tokens, got %d: %+v", len(res.Tokens), res.Tokens)
	}
	lt := res.Tokens[0]
	if lt.Text != "<" || lt.Def.Line != 1 || lt.Def.Col != 14 {
		t.Fatalf("token <: %+v", lt)
	}
	if lt.Call.Line != 2 || lt.Call.Col != 1 {
		t.Fatalf("token < call site = %v, want 2:1", lt.Call)
	}
	if len(lt.Chain) != 1 || lt.Chain[0].Macro != "WRAP" ||
		lt.Chain[0].Def.Line != 1 || lt.Chain[0].Def.Col != 6 ||
		lt.Chain[0].Call.Line != 2 || lt.Chain[0].Call.Col != 1 {
		t.Fatalf("token < chain = %+v", lt.Chain)
	}
	hi := res.Tokens[1]
	if hi.Text != "hi" || hi.Def.Line != 2 || hi.Def.Col != 6 {
		t.Fatalf("token hi: %+v", hi)
	}
	if hi.Call.Line != 2 || hi.Call.Col != 1 || len(hi.Chain) != 1 {
		t.Fatalf("token hi provenance: %+v", hi)
	}
}

func TestFullExpansionChain(t *testing.T) {
	src := `#def INNER() deep
#def OUTER() [INNER()]
OUTER()
`
	res := expandOK(t, src)
	var deep *OutToken
	for i := range res.Tokens {
		if res.Tokens[i].Text == "deep" {
			deep = &res.Tokens[i]
		}
	}
	if deep == nil {
		t.Fatalf("no deep token in %+v", res.Tokens)
	}
	if len(deep.Chain) != 2 || deep.Chain[0].Macro != "OUTER" || deep.Chain[1].Macro != "INNER" {
		t.Fatalf("chain = %+v", deep.Chain)
	}
	if deep.Def.Line != 1 || deep.Def.Col != 14 {
		t.Fatalf("def = %v, want 1:14", deep.Def)
	}
	// Innermost call site: INNER() inside OUTER's body at line 2, col 15.
	if deep.Call.Line != 2 || deep.Call.Col != 15 {
		t.Fatalf("call = %v, want 2:15", deep.Call)
	}
}

func findErr(errs []error, kind ErrKind) *ExpandError {
	for _, err := range errs {
		if ee, ok := err.(*ExpandError); ok && ee.Kind == kind {
			return ee
		}
	}
	return nil
}

func chainNames(chain []Frame) []string {
	names := make([]string, len(chain))
	for i, f := range chain {
		names[i] = f.Macro
	}
	return names
}

func TestCycleEvidence(t *testing.T) {
	src := `#def A(x) B(x)
#def B(x) A(x)
A(1)
`
	res, errs := New(Options{}).Expand("test", src)
	ce := findErr(errs, ErrCycle)
	if ce == nil {
		t.Fatalf("want cycle error, got %v", errs)
	}
	// Shortest evidence: A -> B -> A.
	want := []string{"A", "B", "A"}
	if fmt.Sprint(chainNames(ce.Chain)) != fmt.Sprint(want) {
		t.Fatalf("evidence = %v, want %v", chainNames(ce.Chain), want)
	}
	// The re-entering A call is in B's body at line 2, col 11.
	if ce.Pos.Line != 2 || ce.Pos.Col != 11 {
		t.Fatalf("cycle pos = %v, want 2:11", ce.Pos)
	}
	// The unexpanded call is preserved in the output.
	if res.Text != "A(1)\n" {
		t.Fatalf("output = %q", res.Text)
	}
}

func TestSelfCycleShortestEvidence(t *testing.T) {
	src := `#def R() R()
R()
`
	_, errs := New(Options{}).Expand("test", src)
	ce := findErr(errs, ErrCycle)
	if ce == nil {
		t.Fatalf("want cycle error, got %v", errs)
	}
	if got := chainNames(ce.Chain); len(got) != 2 || got[0] != "R" || got[1] != "R" {
		t.Fatalf("evidence = %v, want [R R]", got)
	}
}

func TestBudgetExhaustion(t *testing.T) {
	var sb strings.Builder
	for i := 0; i <= 30; i++ {
		if i == 30 {
			fmt.Fprintf(&sb, "#def M%d() leaf\n", i)
		} else {
			fmt.Fprintf(&sb, "#def M%d() M%d()\n", i, i+1)
		}
	}
	sb.WriteString("M0()\n")
	res, errs := New(Options{Budget: 5}).Expand("gen", sb.String())
	be := findErr(errs, ErrBudget)
	if be == nil {
		t.Fatalf("want budget error, got %v", errs)
	}
	// Shortest evidence: M0..M5 (5 expansions, the 6th hits the budget).
	if len(be.Chain) != 6 {
		t.Fatalf("evidence len = %d, want 6: %v", len(be.Chain), chainNames(be.Chain))
	}
	for i := 0; i <= 5; i++ {
		if be.Chain[i].Macro != fmt.Sprintf("M%d", i) {
			t.Fatalf("evidence = %v", chainNames(be.Chain))
		}
	}
	// Only one budget error is kept (the shortest).
	count := 0
	for _, err := range errs {
		if ee, ok := err.(*ExpandError); ok && ee.Kind == ErrBudget {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("want 1 budget error, got %d", count)
	}
	// The unexpanded frontier call remains in the output.
	if !strings.Contains(res.Text, "M5()") {
		t.Fatalf("output = %q", res.Text)
	}
}

// An error raised while expanding an argument must be located inside the
// argument text at the call site, with the enclosing call in the chain.
func TestErrorInsideArgument(t *testing.T) {
	src := `#def ONE(x) x
#def TWO(a, b) a b
ONE(TWO(1))
`
	_, errs := New(Options{}).Expand("test", src)
	ae := findErr(errs, ErrArity)
	if ae == nil {
		t.Fatalf("want arity error, got %v", errs)
	}
	// TWO sits at line 3, col 5, inside ONE's argument list.
	if ae.Pos.Line != 3 || ae.Pos.Col != 5 {
		t.Fatalf("error pos = %v, want 3:5", ae.Pos)
	}
	if len(ae.Chain) != 1 || ae.Chain[0].Macro != "ONE" {
		t.Fatalf("error chain = %+v", ae.Chain)
	}
}

func TestUnterminatedArgs(t *testing.T) {
	src := "#def F(x) x\nF(1\n"
	_, errs := New(Options{}).Expand("test", src)
	se := findErr(errs, ErrSyntax)
	if se == nil {
		t.Fatalf("want syntax error, got %v", errs)
	}
	if se.Pos.Line != 2 || se.Pos.Col != 1 {
		t.Fatalf("error pos = %v, want 2:1", se.Pos)
	}
}
