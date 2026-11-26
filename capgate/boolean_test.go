package capgate

import (
	"math/big"
	"testing"
)

func TestMajorityPrimeImplicantsAndDisjointCover(t *testing.T) {
	u, err := buildUniverse(nil, Policy{Features: []string{"x", "y", "z"}, MaxFeatures: 3})
	if err != nil {
		t.Fatal(err)
	}
	// Majority xy || xz || yz. With bit i corresponding to feature i, the
	// onset indexes are 011(3), 101(5), 110(6), 111(7).
	onset := big.NewInt(0)
	for _, idx := range []int{3, 5, 6, 7} {
		onset.SetBit(onset, idx, 1)
	}

	primes := u.primeCubes(onset)
	var exprs []string
	for _, p := range primes {
		exprs = append(exprs, p.Expr())
	}
	want := map[string]bool{"x && y": true, "x && z": true, "y && z": true}
	if len(exprs) != 3 {
		t.Fatalf("primes = %v, want three majority PIs", exprs)
	}
	for _, e := range exprs {
		if !want[e] {
			t.Fatalf("unexpected prime %q in %v", e, exprs)
		}
	}

	blocks := u.disjointCubes(onset, primes)
	if len(blocks) == 0 {
		t.Fatal("no disjoint blocks")
	}
	covered := map[int]int{}
	for bi, b := range blocks {
		set := u.cubeSet(masksOf(b.cube, u.features))
		if hit := new(big.Int).And(set, new(big.Int).AndNot(u.allSet, onset)); hit.BitLen() != 0 {
			// Illegal assignments are don't-cares; here all assignments are
			// legal, so any non-onset hit is a real failure.
			t.Fatalf("block %s covers non-onset assignments", b.cube.Expr())
		}
		for _, idx := range b.indices {
			if prev, ok := covered[idx]; ok {
				t.Fatalf("index %d covered by blocks %d and %d", idx, prev, bi)
			}
			covered[idx] = bi
		}
	}
	if len(covered) != 4 {
		t.Fatalf("covered %d onset assignments, want 4", len(covered))
	}
}

func masksOf(c Cube, features []string) (int, int) {
	return c.masks(features)
}

func TestConditionParser(t *testing.T) {
	e, err := ParseExpr("linux && (arm64 || amd64) && !experimental")
	if err != nil {
		t.Fatal(err)
	}
	if !e.Eval(Assignment{"linux": true, "arm64": true}) {
		t.Fatal("expected arm64 match")
	}
	if e.Eval(Assignment{"linux": true, "amd64": true, "experimental": true}) {
		t.Fatal("experimental should reject")
	}
	if e.Eval(Assignment{"linux": false, "arm64": true}) {
		t.Fatal("linux required")
	}
}
