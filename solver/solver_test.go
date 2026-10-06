package solver

import (
	"strings"
	"testing"

	"musdiag/csp"
)

func parse(t *testing.T, src string) *csp.Problem {
	t.Helper()
	p, err := csp.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return p
}

func names(cons []csp.Constraint) []string {
	out := make([]string, len(cons))
	for i, c := range cons {
		out[i] = c.Name
	}
	return out
}

func TestCheckSAT(t *testing.T) {
	p := parse(t, "var x = 0..2\nvar y = 0..2\nc1: x + y >= 3\nc2: x <= 2\n")
	env, res := Check(p, p.Cons, DefaultLimit)
	if res != SAT {
		t.Fatalf("got %v, want SAT", res)
	}
	for _, c := range p.Cons {
		if !c.Holds(env) {
			t.Fatalf("witness %v violates %s", env, c)
		}
	}
}

func TestCheckUNSAT(t *testing.T) {
	p := parse(t, "var x = 0..1\nc1: x >= 1\nc2: x <= 0\nc3: x == 0\n")
	if _, res := Check(p, p.Cons, DefaultLimit); res != UNSAT {
		t.Fatalf("got %v, want UNSAT", res)
	}
}

func TestCheckUnknown(t *testing.T) {
	p := parse(t, "var x = 0..99\nvar y = 0..99\nc: x + y >= 0\n")
	if _, res := Check(p, p.Cons, 10); res != Unknown {
		t.Fatalf("got %v, want Unknown", res)
	}
}

func TestFindMUS(t *testing.T) {
	p := parse(t, `var x = 0..2
var y = 0..2
var z = 0..1
c1: x + y >= 4
c2: x <= 1
c3: y <= 1
c4: z == 1
`)
	mus, _, res := FindMUS(p, DefaultLimit)
	if res != UNSAT {
		t.Fatalf("got %v, want UNSAT", res)
	}
	got := names(mus)
	if len(got) != 2 || got[0] != "c1" || got[1] != "c3" {
		t.Fatalf("MUS = %v, want [c1 c3]", got)
	}
	witnesses, ok := VerifyMUS(p, mus, DefaultLimit)
	if !ok {
		t.Fatal("VerifyMUS failed")
	}
	if len(witnesses) != 2 {
		t.Fatalf("want 2 deletion witnesses, got %d", len(witnesses))
	}
}

// The deletion pass runs in input order, so the same constraints in a
// different order can yield a different MUS. Here {c1,c2} and {c2,c3}
// are both minimal unsatisfiable; which one survives depends on order.
func TestFindMUSOrderDetermined(t *testing.T) {
	musOf := func(src string) []string {
		mus, _, res := FindMUS(parse(t, src), DefaultLimit)
		if res != UNSAT {
			t.Fatalf("got %v, want UNSAT", res)
		}
		return names(mus)
	}
	got := musOf("var x = 0..1\nc1: x >= 1\nc2: x <= 0\nc3: x >= 1\n")
	if len(got) != 2 || got[0] != "c2" || got[1] != "c3" {
		t.Fatalf("order c1,c2,c3: MUS = %v, want [c2 c3]", got)
	}
	got = musOf("var x = 0..1\nc3: x >= 1\nc2: x <= 0\nc1: x >= 1\n")
	if len(got) != 2 || got[0] != "c2" || got[1] != "c1" {
		t.Fatalf("order c3,c2,c1: MUS = %v, want [c2 c1]", got)
	}
}

func TestFindMUSSatisfiable(t *testing.T) {
	p := parse(t, "var x = 0..2\nc1: x >= 1\nc2: x <= 2\n")
	mus, witness, res := FindMUS(p, DefaultLimit)
	if res != SAT || mus != nil || witness == nil {
		t.Fatalf("got mus=%v witness=%v res=%v, want SAT with witness", mus, witness, res)
	}
}

// A MUS need not be cardinality-minimal: the deletion pass keeps
// {c2,c3,c4} (size 3) even though {c1,c5} (size 2) is also an
// unsatisfiable subset. c1 and c5 are dropped first because the rest is
// still unsatisfiable without them.
func TestMUSNotCardinalityMinimal(t *testing.T) {
	p := parse(t, `var x = 0..2
var y = 0..2
var z = 0..1
c1: z >= 1
c2: x + y >= 3
c3: x <= 1
c4: y <= 1
c5: z <= 0
`)
	mus, _, res := FindMUS(p, DefaultLimit)
	if res != UNSAT {
		t.Fatalf("got %v, want UNSAT", res)
	}
	got := names(mus)
	if len(got) != 3 || got[0] != "c2" || got[1] != "c3" || got[2] != "c4" {
		t.Fatalf("MUS = %v, want [c2 c3 c4]", got)
	}
	if _, ok := VerifyMUS(p, mus, DefaultLimit); !ok {
		t.Fatal("VerifyMUS failed")
	}
	// The smaller MUS {c1,c5} really is unsatisfiable.
	if _, r := Check(p, []csp.Constraint{p.Cons[0], p.Cons[4]}, DefaultLimit); r != UNSAT {
		t.Fatalf("{c1,c5}: got %v, want UNSAT", r)
	}
}
