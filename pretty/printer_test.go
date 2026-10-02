package pretty

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// deepExpr is a deeply nested, complex expression exercising every node
// type and every precedence level.
const deepExpr = `compute(alpha + beta * gamma - delta / (epsilon + zeta), ` +
	`transform(x1, x2, x3) * (foo(bar(1, 2), baz(3)) + quux) % 17, ` +
	`!(flag1 && flag2) || (score >= threshold && level <= max_level) == done, ` +
	`-"negated" + wrap(nested(deep(deeper(deepest(a, b), c), d), e), f) * 3.14)`

// roundTrip checks parse -> print -> parse yields an identical AST, and
// that printing is idempotent.
func roundTrip(t *testing.T, src string, width int) (Expr, string) {
	t.Helper()
	ast1, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out := PrintWidth(ast1, width)
	ast2, err := Parse(out)
	if err != nil {
		t.Fatalf("re-parse of printed output failed: %v\noutput:\n%s", err, out)
	}
	if !reflect.DeepEqual(ast1, ast2) {
		t.Fatalf("AST mismatch after round trip\ninput:  %s\noutput:\n%s", src, out)
	}
	if out2 := PrintWidth(ast2, width); out2 != out {
		t.Fatalf("printing not idempotent\nfirst:\n%s\nsecond:\n%s", out, out2)
	}
	return ast1, out
}

func TestRoundTrip(t *testing.T) {
	exprs := []string{
		"1",
		"x",
		`"hello"`,
		"a + b * c",
		"(a + b) * c",
		"a - (b - c)",
		"a + (b + c)",
		"-(a + b) * !c",
		"f()",
		"f(a, b, c)(d)(e, f)",
		"a == b && c != d || e < f && g >= h",
		"1.5 * (x + 2.25) / (y - 3.75) % 4",
		deepExpr,
	}
	for _, src := range exprs {
		roundTrip(t, src, DefaultWidth)
	}
}

func TestWidthLimit(t *testing.T) {
	_, out := roundTrip(t, deepExpr, DefaultWidth)
	for i, line := range strings.Split(out, "\n") {
		if len(line) > DefaultWidth {
			t.Errorf("line %d exceeds %d columns (%d): %q",
				i+1, DefaultWidth, len(line), line)
		}
	}
	if !strings.Contains(out, "\n") {
		t.Errorf("expected line breaks for long expression, got:\n%s", out)
	}
}

func TestBreaksAtOperatorsAndCommas(t *testing.T) {
	src := "aaaaaaaaaa + bbbbbbbbbb + cccccccccc + dddddddddd + eeeeeeeeee + ffffffffff"
	_, out := roundTrip(t, src, 40)
	for _, line := range strings.Split(out, "\n") {
		if len(line) > 40 {
			t.Errorf("line exceeds 40 columns: %q", line)
		}
	}
	// Continuation lines must be indented.
	lines := strings.Split(out, "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[1], indentUnit) {
		t.Errorf("expected indented continuation, got:\n%s", out)
	}
}

// TestFuzzRoundTrip generates random ASTs, prints them, re-parses the
// output and requires structural identity with the original AST.
func TestFuzzRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for i := 0; i < 500; i++ {
		ast := randExpr(r, 6)
		out := Print(ast)
		back, err := Parse(out)
		if err != nil {
			t.Fatalf("iter %d: re-parse failed: %v\noutput:\n%s", i, err, out)
		}
		if !reflect.DeepEqual(ast, back) {
			t.Fatalf("iter %d: AST mismatch\nprinted:\n%s\ngot:  %#v\nwant: %#v",
				i, out, back, ast)
		}
	}
}

var randOps = []string{"+", "-", "*", "/", "%", "==", "!=", "<", "<=", ">", ">=", "&&", "||"}

func randExpr(r *rand.Rand, depth int) Expr {
	if depth <= 0 {
		switch r.Intn(3) {
		case 0:
			return &Number{Value: string('0' + byte(r.Intn(10)))}
		case 1:
			return &Ident{Name: string('a' + byte(r.Intn(26)))}
		default:
			return &String{Value: string('a' + byte(r.Intn(26)))}
		}
	}
	switch r.Intn(5) {
	case 0:
		return &Unary{Op: map[int]string{0: "-", 1: "!"}[r.Intn(2)], X: randExpr(r, depth-1)}
	case 1, 2:
		return &Binary{Op: randOps[r.Intn(len(randOps))],
			X: randExpr(r, depth-1), Y: randExpr(r, depth-1)}
	case 3:
		n := r.Intn(4)
		var args []Expr // keep nil for zero args, matching the parser
		if n > 0 {
			args = make([]Expr, n)
		}
		for i := range args {
			args[i] = randExpr(r, depth-1)
		}
		return &Call{Func: randExpr(r, depth-1), Args: args}
	default:
		return randExpr(r, depth-1)
	}
}
