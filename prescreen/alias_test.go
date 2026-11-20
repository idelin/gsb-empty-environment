package prescreen

import (
	"math/rand"
	"testing"
)

// TestAliasCountMatchesOracle6 crosses random rank-deficient maps with random
// affine request sets in the 6-bit reference space and asserts the algebraic
// distinct-block count equals a brute-force enumeration, including when two
// disjoint logical slices collapse onto the same physical image.
func TestAliasCountMatchesOracle6(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 200; iter++ {
		m := randMap6(rng)
		n := 1 + rng.Intn(4)
		logical := make([]Affine, n)
		images := make([]Affine, n)
		oracleUnion := map[Vec]struct{}{}
		for i := range logical {
			logical[i] = randAffine6(rng)
			images[i] = m.Apply(logical[i])
			for p := range oracleApply(m, logical[i]) {
				oracleUnion[p] = struct{}{}
			}
		}
		proof := UnionInclusionExclusion(images)
		if proof.UnionSize != uint64(len(oracleUnion)) {
			t.Fatalf("alias union algebra=%d oracle=%d rank=%d",
				proof.UnionSize, len(oracleUnion), m.Rank())
		}
		// Every individual image must match its enumerated point set.
		for i, img := range images {
			if !samePointSet(pointSet(img), oracleApply(m, logical[i])) {
				t.Fatalf("image %d differs from oracle", i)
			}
		}
	}
}

// TestDeliberateAliasing constructs two provably disjoint logical singletons
// that a kernel vector maps to the identical physical block, then checks the
// ticket counts one distinct block rather than two.
func TestDeliberateAliasing(t *testing.T) {
	rng := rand.New(rand.NewSource(8))
	for iter := 0; iter < 50; iter++ {
		m := randMap6(rng)
		if m.Rank() >= 6 {
			continue // need a non-trivial kernel
		}
		kernel := findKernelVector(m)
		if kernel == 0 {
			continue
		}
		a := Point(0)
		b := Point(kernel)
		if a.Equal(b) {
			t.Fatal("kernel vector must be nonzero, making the points disjoint")
		}
		imgA := m.Apply(a)
		imgB := m.Apply(b)
		if !imgA.Equal(imgB) || imgA.Size() != 1 {
			t.Fatal("kernel vector must alias both singletons onto one block")
		}
		proof := UnionInclusionExclusion([]Affine{imgA, imgB})
		if proof.UnionSize != 1 {
			t.Fatalf("aliased distinct count = %d, want 1", proof.UnionSize)
		}
		return
	}
	t.Skip("no rank-deficient map drawn")
}

// findKernelVector returns a nonzero vector in the kernel of m's linear part,
// or zero when the map is full rank. It solves the homogeneous system Mv = 0.
func findKernelVector(m AffineMap) Vec {
	eqs := make([]Eq, 0, Width)
	for i := 0; i < Width; i++ {
		if m.Rows[i] == 0 {
			continue
		}
		eqs = append(eqs, MakeEq(m.Rows[i], 0))
	}
	kernel := SolveAffine(eqs)
	if kernel.Empty || len(kernel.Dir) == 0 {
		return 0
	}
	return kernel.Dir[0]
}
