package pst

import (
	"math/big"
	"math/rand"
	"testing"
)

// naive is the reference model: one fully-copied array per version.
type naive struct {
	m   int64
	ver [][]int64
}

func newNaive(n, m int64) *naive {
	return &naive{m: m, ver: [][]int64{make([]int64, n)}}
}

func (nv *naive) assign(ver int, l, r, v int64) int {
	if l == r {
		return ver
	}
	cp := append([]int64(nil), nv.ver[ver]...)
	for i := l; i < r; i++ {
		cp[i] = norm(v, nv.m)
	}
	nv.ver = append(nv.ver, cp)
	return len(nv.ver) - 1
}

func (nv *naive) affine(ver int, l, r, mul, add int64) int {
	if l == r {
		return ver
	}
	cp := append([]int64(nil), nv.ver[ver]...)
	mu, ad := norm(mul, nv.m), norm(add, nv.m)
	for i := l; i < r; i++ {
		cp[i] = addMod(mulMod(cp[i], mu, nv.m), ad, nv.m)
	}
	nv.ver = append(nv.ver, cp)
	return len(nv.ver) - 1
}

func (nv *naive) sum(ver int, l, r int64) int64 {
	var s int64
	for i := l; i < r; i++ {
		s = addMod(s, nv.ver[ver][i], nv.m)
	}
	return s
}

func checkAll(t *testing.T, tr *Tree, nv *naive, n int64, rng *rand.Rand) {
	t.Helper()
	for q := 0; q < 8; q++ {
		ver := rng.Intn(tr.Versions())
		l := int64(rng.Intn(int(n) + 1))
		r := l + int64(rng.Intn(int(n-l)+1))
		if got, want := tr.Sum(ver, l, r), nv.sum(ver, l, r); got != want {
			t.Fatalf("Sum(v%d, %d, %d) = %d, want %d", ver, l, r, got, want)
		}
	}
}

// TestRandomizedAgainstNaive cross-checks every operation against the naive
// per-version arrays, across prime, composite, tiny, and huge moduli.
func TestRandomizedAgainstNaive(t *testing.T) {
	moduli := []int64{1, 2, 7, 12, 100, 1_000_000_007, 1_000_000_000, 1 << 31, 1 << 62}
	sizes := []int64{1, 2, 3, 17, 64}
	for _, m := range moduli {
		for _, n := range sizes {
			rng := rand.New(rand.NewSource(m*1000 + n))
			tr := NewTree(n, m)
			nv := newNaive(n, m)
			for step := 0; step < 300; step++ {
				ver := rng.Intn(tr.Versions())
				l := int64(rng.Intn(int(n) + 1))
				r := l + int64(rng.Intn(int(n-l)+1))
				// Deliberately nasty parameters: negatives and values
				// far outside [0, m).
				param := func() int64 {
					switch rng.Intn(4) {
					case 0:
						return -rng.Int63n(1 << 62)
					case 1:
						return rng.Int63n(1 << 62)
					default:
						return rng.Int63n(m)
					}
				}
				switch rng.Intn(3) {
				case 0:
					v := param()
					if got := tr.Assign(ver, l, r, v); got != nv.assign(ver, l, r, v) {
						t.Fatalf("m=%d n=%d: version id mismatch on Assign", m, n)
					}
				case 1:
					mu, ad := param(), param()
					if got := tr.Affine(ver, l, r, mu, ad); got != nv.affine(ver, l, r, mu, ad) {
						t.Fatalf("m=%d n=%d: version id mismatch on Affine", m, n)
					}
				default:
					if got, want := tr.Sum(ver, l, r), nv.sum(ver, l, r); got != want {
						t.Fatalf("m=%d n=%d: Sum(v%d, %d, %d) = %d, want %d", m, n, ver, l, r, got, want)
					}
				}
				checkAll(t, tr, nv, n, rng)
			}
		}
	}
}

// TestAssignThenMultiplyZero checks that assign followed by a zero multiply
// wipes the range, and that the reversed order does not (tag order matters).
func TestAssignThenMultiplyZero(t *testing.T) {
	const n, m = 10, 100 // composite modulus
	tr := NewTree(n, m)
	nv := newNaive(n, m)

	v1, n1 := tr.Assign(0, 2, 8, 7), nv.assign(0, 2, 8, 7)
	v2, n2 := tr.Affine(v1, 0, n, 0, 0), nv.affine(n1, 0, n, 0, 0)
	if got := tr.Sum(v2, 0, n); got != 0 || nv.sum(n2, 0, n) != 0 {
		t.Fatalf("assign-then-mul-zero: got %d, want 0", got)
	}
	// Older version must be untouched.
	if got, want := tr.Sum(v1, 0, n), nv.sum(n1, 0, n); got != want || got != 42 {
		t.Fatalf("v1 corrupted: got %d, want %d", got, want)
	}
	// Reversed order: multiply by zero first, then assign -> survives.
	v3, n3 := tr.Affine(0, 0, n, 0, 0), nv.affine(0, 0, n, 0, 0)
	v4, n4 := tr.Assign(v3, 2, 8, 7), nv.assign(n3, 2, 8, 7)
	if got, want := tr.Sum(v4, 0, n), nv.sum(n4, 0, n); got != want || got != 42 {
		t.Fatalf("mul-zero-then-assign: got %d, want %d", got, want)
	}
	// Partial overlap: zero out only the middle of an assigned range.
	v5, n5 := tr.Assign(0, 0, n, 9), nv.assign(0, 0, n, 9)
	v6, n6 := tr.Affine(v5, 3, 7, 0, 1), nv.affine(n5, 3, 7, 0, 1)
	if got, want := tr.Sum(v6, 0, n), nv.sum(n6, 0, n); got != want {
		t.Fatalf("partial zero-out: got %d, want %d", got, want)
	}
}

// TestNegativeNormalization verifies negative values and affine parameters
// are normalized into [0, m).
func TestNegativeNormalization(t *testing.T) {
	const n, m = 6, 7
	tr := NewTree(n, m)
	nv := newNaive(n, m)

	v1, n1 := tr.Assign(0, 0, n, -1), nv.assign(0, 0, n, -1)
	v2, n2 := tr.Affine(v1, 1, 5, -3, -10), nv.affine(n1, 1, 5, -3, -10)
	v3, n3 := tr.Affine(v2, 0, n, -8, 13), nv.affine(n2, 0, n, -8, 13)
	for i, pair := range [][2]int{{v1, n1}, {v2, n2}, {v3, n3}} {
		for l := int64(0); l <= n; l++ {
			for r := l; r <= n; r++ {
				if got, want := tr.Sum(pair[0], l, r), nv.sum(pair[1], l, r); got != want {
					t.Fatalf("version %d: Sum(%d, %d) = %d, want %d", i, l, r, got, want)
				}
			}
		}
	}
	// Spot check: -1 mod 7 == 6.
	if got := tr.Sum(v1, 0, 1); got != 6 {
		t.Fatalf("norm(-1) = %d, want 6", got)
	}
}

// TestForkedInterleavedUpdates forks two branches from a common base and
// alternates updates between them; every version must stay consistent.
func TestForkedInterleavedUpdates(t *testing.T) {
	const n, m = 24, 1_000_000_000 // composite
	rng := rand.New(rand.NewSource(99))
	tr := NewTree(n, m)
	nv := newNaive(n, m)

	base, nbase := tr.Assign(0, 0, n, 5), nv.assign(0, 0, n, 5)
	branchA, nBranchA := tr.Affine(base, 0, 12, 3, 1), nv.affine(nbase, 0, 12, 3, 1)
	branchB, nBranchB := tr.Assign(base, 8, 20, 2), nv.assign(nbase, 8, 20, 2)

	for round := 0; round < 60; round++ {
		l := int64(rng.Intn(n + 1))
		r := l + int64(rng.Intn(int(n-l)+1))
		if round%2 == 0 {
			mu, ad := rng.Int63n(10)-5, rng.Int63n(20)-10
			branchA, nBranchA = tr.Affine(branchA, l, r, mu, ad), nv.affine(nBranchA, l, r, mu, ad)
		} else {
			v := rng.Int63n(20) - 10
			branchB, nBranchB = tr.Assign(branchB, l, r, v), nv.assign(nBranchB, l, r, v)
		}
		// Full scan of every version after each round.
		for ver := 0; ver < tr.Versions(); ver++ {
			if got, want := tr.Sum(ver, 0, n), nv.sum(ver, 0, n); got != want {
				t.Fatalf("round %d: version %d sum = %d, want %d", round, ver, got, want)
			}
		}
	}
}

// TestZeroLengthIntervals checks that empty ranges are legal no-ops.
func TestZeroLengthIntervals(t *testing.T) {
	tr := NewTree(5, 11)
	if v := tr.Assign(0, 3, 3, 99); v != 0 {
		t.Fatalf("zero-length Assign created version %d", v)
	}
	if v := tr.Affine(0, 0, 0, 0, 0); v != 0 {
		t.Fatalf("zero-length Affine created version %d", v)
	}
	if v := tr.Affine(0, 5, 5, 7, 9); v != 0 {
		t.Fatalf("zero-length Affine at end created version %d", v)
	}
	if got := tr.Sum(0, 2, 2); got != 0 {
		t.Fatalf("zero-length Sum = %d, want 0", got)
	}
	// Empty array entirely.
	empty := NewTree(0, 3)
	if got := empty.Sum(0, 0, 0); got != 0 {
		t.Fatalf("empty tree Sum = %d, want 0", got)
	}
	if v := empty.Assign(0, 0, 0, 1); v != 0 {
		t.Fatalf("empty tree Assign created version %d", v)
	}
}

// TestBuildFromArray verifies NewTreeFrom normalizes initial values.
func TestBuildFromArray(t *testing.T) {
	a := []int64{-3, 8, -14, 0, 25}
	tr := NewTreeFrom(a, 6)
	nv := &naive{m: 6, ver: [][]int64{{3, 2, 4, 0, 1}}}
	for l := int64(0); l <= 5; l++ {
		for r := l; r <= 5; r++ {
			if got, want := tr.Sum(0, l, r), nv.sum(0, l, r); got != want {
				t.Fatalf("Sum(0, %d, %d) = %d, want %d", l, r, got, want)
			}
		}
	}
}

// TestModulusOne checks the degenerate modulus where everything is 0.
func TestModulusOne(t *testing.T) {
	tr := NewTree(4, 1)
	v := tr.Assign(0, 0, 4, 12345)
	v = tr.Affine(v, 1, 3, 678, 9012)
	if got := tr.Sum(v, 0, 4); got != 0 {
		t.Fatalf("mod 1 sum = %d, want 0", got)
	}
}

// TestMulModAgainstBigInt validates the overflow-safe modular multiplication
// against math/big, including moduli near MaxInt64.
func TestMulModAgainstBigInt(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 20000; i++ {
		var m int64
		switch i % 4 {
		case 0:
			m = 1 + rng.Int63n(1<<31) // small
		case 1:
			m = 1 + rng.Int63() // full range
		case 2:
			m = (1 << 63) - 25 - int64(i) // near MaxInt64
		default:
			m = int64(i%1000) + 1 // tiny
		}
		a, b := rng.Int63n(m), rng.Int63n(m)
		got := mulMod(a, b, m)
		bi := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
		bi.Mod(bi, big.NewInt(m))
		if got != bi.Int64() {
			t.Fatalf("mulMod(%d, %d, %d) = %d, want %d", a, b, m, got, bi.Int64())
		}
	}
}
