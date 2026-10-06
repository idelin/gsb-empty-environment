package csp

import (
	"strings"
	"testing"
)

const sample = `
# comment
var x = 0..2
var y = -1..1

c1: x + y >= 2
x * 2 - y != 3
c3: (x - y) * (x + y) <= 4
`

func TestParse(t *testing.T) {
	p, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(p.Vars) != 2 || p.Vars[0].Name != "x" || p.Vars[1].Lo != -1 || p.Vars[1].Hi != 1 {
		t.Fatalf("unexpected vars: %+v", p.Vars)
	}
	if len(p.Cons) != 3 {
		t.Fatalf("want 3 constraints, got %d", len(p.Cons))
	}
	if p.Cons[1].Name != "c2" {
		t.Fatalf("auto-naming: got %q, want c2", p.Cons[1].Name)
	}
	if got := p.Combinations(1000); got != 9 {
		t.Fatalf("combinations: got %d, want 9", got)
	}
}

func TestEval(t *testing.T) {
	p, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	env := map[string]int{"x": 2, "y": 0}
	for i, want := range []bool{true, true, true} {
		if got := p.Cons[i].Holds(env); got != want {
			t.Errorf("constraint %s: got %v, want %v", p.Cons[i].Name, got, want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, src := range []string{
		"var x = 3..1\n",               // empty domain
		"var x = 0..1\nvar x = 0..2\n", // duplicate var
		"var x = 0..1\nc: y >= 1\n",    // undeclared variable
		"var x = 0..1\nc: x + >= 1\n",  // malformed expression
		"var x = 0..1\nc: x + 1\n",     // no relation
	} {
		if _, err := Parse(strings.NewReader(src)); err == nil {
			t.Errorf("expected parse error for %q", src)
		}
	}
}

func TestTooManyConstraints(t *testing.T) {
	var b strings.Builder
	b.WriteString("var x = 0..1\n")
	for i := 0; i < 21; i++ {
		b.WriteString("x >= 0\n")
	}
	if _, err := Parse(strings.NewReader(b.String())); err == nil {
		t.Fatal("expected error for 21 constraints")
	}
}
