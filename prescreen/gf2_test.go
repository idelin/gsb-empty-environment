package prescreen

import (
	"math/rand"
	"testing"
)

func TestSolveRoundTripAndEquations(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 300; iter++ {
		af := randAffine6(rng)
		solved := SolveAffine(af.Equations())
		if !af.Equal(solved) {
			t.Fatalf("equation round trip mismatch: %+v vs %+v", af, solved)
		}
		for _, p := range enumerate(af) {
			for _, eq := range af.Equations() {
				got := parity(uint64(p) & uint64(eq.Coeff()))
				if got != uint64(eq.RHS()) {
					t.Fatalf("point %b violates equation", p)
				}
			}
			if !af.Contains(p) {
				t.Fatalf("Contains misses own point %b", p)
			}
		}
		if af.Size() != uint64(len(enumerate(af))) {
			t.Fatalf("size mismatch")
		}
	}
}

func parity(x uint64) uint64 { return uint64(onesCount(x) & 1) }

func onesCount(x uint64) int {
	n := 0
	for x != 0 {
		n += int(x & 1)
		x >>= 1
	}
	return n
}

func TestIntersectMatchesOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for iter := 0; iter < 300; iter++ {
		a, b := randAffine6(rng), randAffine6(rng)
		got := pointSet(a.Intersect(b))
		want := map[Vec]struct{}{}
		for p := range pointSet(a) {
			if b.Contains(p) {
				want[p] = struct{}{}
			}
		}
		if !samePointSet(got, want) {
			t.Fatalf("intersection mismatch")
		}
	}
}

func TestMapApplyMatchesOracleAndAliasCount(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for iter := 0; iter < 300; iter++ {
		m := randMap6(rng)
		af := randAffine6(rng)
		img := m.Apply(af)
		want := oracleApply(m, af)
		if !samePointSet(pointSet(img), want) {
			t.Fatalf("affine image differs from enumerated image")
		}
		if img.Size() != uint64(len(want)) {
			t.Fatalf("alias count wrong: rank=%d got=%d want=%d",
				m.Rank(), img.Size(), len(want))
		}
	}
}

func TestUnionInclusionExclusionMatchesOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	for iter := 0; iter < 400; iter++ {
		n := 1 + rng.Intn(MaxSlices)
		sets := make([]Affine, n)
		for i := range sets {
			sets[i] = randAffine6(rng)
		}
		proof := UnionInclusionExclusion(sets)
		if proof.UnionSize != oracleUnionSize(sets) {
			t.Fatalf("IE union %d != oracle %d", proof.UnionSize, oracleUnionSize(sets))
		}
		var recomputed uint64
		for _, term := range proof.Terms {
			if term.Sign == 0 {
				t.Fatal("term sign must be ±1")
			}
			if term.Sign > 0 {
				recomputed += term.Aff.Size()
			} else {
				recomputed -= term.Aff.Size()
			}
		}
		if recomputed != proof.UnionSize {
			t.Fatal("certificate terms do not sum to union size")
		}
	}
}

func TestCoverUnionSemantics(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for iter := 0; iter < 300; iter++ {
		target := randAffine6(rng)
		n := 1 + rng.Intn(MaxSlices)
		covers := make([]Affine, n)
		for i := range covers {
			covers[i] = randAffine6(rng)
		}
		_, _, _, ok := CoverUnion(target, covers)

		targetPts := pointSet(target)
		unionPts := map[Vec]struct{}{}
		for _, c := range covers {
			for p := range pointSet(c) {
				unionPts[p] = struct{}{}
			}
		}
		want := true
		for p := range targetPts {
			if _, in := unionPts[p]; !in {
				want = false
			}
		}
		if ok != want {
			t.Fatalf("coverage algebra=%v oracle=%v", ok, want)
		}
	}
}

func TestEmptySetHandling(t *testing.T) {
	empty := EmptyAffine()
	if empty.Size() != 0 || empty.Dim() != -1 {
		t.Fatal("empty set metrics wrong")
	}
	if !empty.Intersect(FullSpace()).Empty {
		t.Fatal("intersection with empty must be empty")
	}
	if IdentityMap().Apply(empty).Empty != true {
		t.Fatal("image of empty must be empty")
	}
	// An inconsistent 0 = 1 system yields the empty set.
	if got := SolveAffine([]Eq{MakeEq(0, 1)}); !got.Empty {
		t.Fatal("inconsistent system not empty")
	}
	proof := UnionInclusionExclusion(nil)
	if proof.UnionSize != 0 {
		t.Fatal("union of nothing must be zero")
	}
	if _, _, _, ok := CoverUnion(empty, nil); !ok {
		t.Fatal("empty covered set should trivially pass")
	}
}

func TestSameSetDifferentEquationRepresentation(t *testing.T) {
	rng := rand.New(rand.NewSource(6))
	for iter := 0; iter < 200; iter++ {
		af := randAffine6(rng)
		eqs := af.Equations()
		// Add redundant linear combinations and reorder; semantics must hold.
		if len(eqs) >= 2 {
			coeff := uint64(eqs[0].Coeff()) ^ uint64(eqs[1].Coeff())
			rhs := (uint64(eqs[0].RHS()) ^ uint64(eqs[1].RHS())) << Width
			combo := Eq(coeff | rhs)
			eqs = append(eqs, combo)
		}
		rng.Shuffle(len(eqs), func(i, j int) { eqs[i], eqs[j] = eqs[j], eqs[i] })
		again := SolveAffine(eqs)
		if !af.Equal(again) {
			t.Fatalf("same set with redundant/reordered equations differs")
		}
	}
}
