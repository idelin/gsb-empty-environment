package main

import (
	"math/big"
	"testing"
)

func TestSelfCases(t *testing.T) {
	for _, c := range buildSelfCases() {
		rep, err := analyze(c.f, c.g, defaultEps())
		if err != nil {
			t.Errorf("%s: analyze error: %v", c.name, err)
			continue
		}
		for _, m := range c.check(rep) {
			t.Errorf("%s: %s", c.name, m)
		}
	}
}

func TestPolyGCD(t *testing.T) {
	// (x-1)^2 (x-2) and (x-1) (x-3) share exactly (x-1).
	a, err := parsePoly([]string{"-2", "5", "-4", "1"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := parsePoly([]string{"3", "-4", "1"})
	if err != nil {
		t.Fatal(err)
	}
	g := gcdPoly(a, b)
	want, _ := parsePoly([]string{"-1", "1"})
	if g.String() != want.String() {
		t.Errorf("gcd = %s, want %s", g, want)
	}
}

func TestSquarefreeFactors(t *testing.T) {
	// (x-1)^3 (x+2)^2: factors by multiplicity are (x+2) then (x-1).
	f, err := parsePoly([]string{"-4", "8", "-1", "-5", "1", "1"})
	if err != nil {
		t.Fatal(err)
	}
	facs := squarefreeFactors(f)
	if len(facs) != 3 {
		t.Fatalf("want 3 factor slots, got %d", len(facs))
	}
	if evalPoly(facs[1], big.NewRat(-2, 1)).Sign() != 0 {
		t.Errorf("slot 2 should vanish at x=-2, got %s", facs[1])
	}
	if evalPoly(facs[2], big.NewRat(1, 1)).Sign() != 0 {
		t.Errorf("slot 3 should vanish at x=1, got %s", facs[2])
	}
}

func TestSturmCountOpen(t *testing.T) {
	// p = (x-1)^2 (x-2): distinct roots at 1 and 2.
	p, err := parsePoly([]string{"-2", "5", "-4", "1"})
	if err != nil {
		t.Fatal(err)
	}
	st := newSturm(p)
	if n := st.countOpen(mustRat("0"), mustRat("3")); n != 2 {
		t.Errorf("roots in (0,3) = %d, want 2", n)
	}
	if n := st.countOpen(mustRat("0"), mustRat("1")); n != 0 {
		t.Errorf("roots in open (0,1) = %d, want 0 (endpoint excluded)", n)
	}
	if n := st.countOpen(mustRat("1"), mustRat("2")); n != 0 {
		t.Errorf("roots in open (1,2) = %d, want 0 (endpoints excluded)", n)
	}
	if n := st.countOpen(mustRat("3/2"), mustRat("5/2")); n != 1 {
		t.Errorf("roots in (3/2,5/2) = %d, want 1", n)
	}
}
