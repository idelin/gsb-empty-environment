package segtree

import (
	"math/rand"
	"testing"
)

// model is the naive per-version array implementation used as oracle.
type model struct {
	mod      int64
	versions [][]int64
}

func newModel(n int, mod int64) *model {
	return &model{mod: mod, versions: [][]int64{make([]int64, n)}}
}

func (m *model) norm(v int64) int64 {
	v %= m.mod
	if v < 0 {
		v += m.mod
	}
	return v
}

func (m *model) assign(ver, l, r int, v int64) int {
	nv := append([]int64(nil), m.versions[ver]...)
	for i := l; i < r; i++ {
		nv[i] = m.norm(v)
	}
	m.versions = append(m.versions, nv)
	return len(m.versions) - 1
}

func (m *model) affine(ver, l, r int, mul, add int64) int {
	mul, add = m.norm(mul), m.norm(add)
	nv := append([]int64(nil), m.versions[ver]...)
	for i := l; i < r; i++ {
		nv[i] = (nv[i]*mul + add) % m.mod
	}
	m.versions = append(m.versions, nv)
	return len(m.versions) - 1
}

func (m *model) sum(ver, l, r int) int64 {
	var s int64
	for _, v := range m.versions[ver][l:r] {
		s = (s + v) % m.mod
	}
	return s
}

func checkAllRanges(t *testing.T, tree *Tree, m *model, ver, n int) {
	t.Helper()
	for l := 0; l <= n; l++ {
		for r := l; r <= n; r++ {
			got, want := tree.Sum(ver, l, r), m.sum(ver, l, r)
			if got != want {
				t.Fatalf("version %d sum[%d,%d): got %d, want %d", ver, l, r, got, want)
			}
		}
	}
}

// pair ties a tree version to the matching model version; indices must agree.
func pair(t *testing.T, tv, mv int) int {
	t.Helper()
	if tv != mv {
		t.Fatalf("version index mismatch: tree %d, model %d", tv, mv)
	}
	return tv
}

// TestAssignThenMulZero pins the non-commutative tag order: an assign must
// wipe older tags, and a later affine (even mul=0) applies on top of it.
func TestAssignThenMulZero(t *testing.T) {
	const mod = 7
	tree := New(5, mod)
	m := newModel(5, mod)

	v1 := pair(t, tree.Assign(0, 0, 5, 3), m.assign(0, 0, 5, 3))         // [3 3 3 3 3]
	v2 := pair(t, tree.Affine(v1, 0, 5, 0, 0), m.affine(v1, 0, 5, 0, 0)) // mul by zero -> all zero
	v3 := pair(t, tree.Affine(v1, 1, 4, 0, 2), m.affine(v1, 1, 4, 0, 2)) // [3 2 2 2 3]
	v4 := pair(t, tree.Assign(v1, 1, 4, 9), m.assign(v1, 1, 4, 9))       // assign overrides (9%7=2)
	v5 := pair(t, tree.Affine(v4, 0, 5, 2, 1), m.affine(v4, 0, 5, 2, 1)) // affine on top of assign

	for _, ver := range []int{v1, v2, v3, v4, v5} {
		checkAllRanges(t, tree, m, ver, 5)
	}
	if got := tree.Sum(v2, 0, 5); got != 0 {
		t.Fatalf("mul-by-zero version sum = %d, want 0", got)
	}
	if got := tree.Sum(v3, 0, 5); got != (3+2+2+2+3)%mod {
		t.Fatalf("sum = %d, want %d", got, (3+2+2+2+3)%mod)
	}
}

// TestNegativeNormalization feeds negative values and coefficients.
func TestNegativeNormalization(t *testing.T) {
	const mod = 7
	tree := New(6, mod)
	m := newModel(6, mod)

	v1 := pair(t, tree.Assign(0, 0, 6, -1), m.assign(0, 0, 6, -1)) // -1 mod 7 = 6
	v2 := pair(t, tree.Affine(v1, 0, 6, -2, -3), m.affine(v1, 0, 6, -2, -3))
	v3 := pair(t, tree.Assign(v2, 2, 4, -1000000000000), m.assign(v2, 2, 4, -1000000000000))
	v4 := pair(t, tree.Affine(v3, 1, 5, -13, 20), m.affine(v3, 1, 5, -13, 20))

	for _, ver := range []int{v1, v2, v3, v4} {
		checkAllRanges(t, tree, m, ver, 6)
	}
	if got := tree.Sum(v1, 0, 6); got != 36%mod {
		t.Fatalf("sum = %d, want %d", got, 36%mod)
	}
}

// TestForkedInterleavedUpdates forks versions and updates them alternately,
// ensuring versions never leak into each other.
func TestForkedInterleavedUpdates(t *testing.T) {
	const mod = 1000
	tree := New(8, mod)
	m := newModel(8, mod)

	v1 := pair(t, tree.Assign(0, 0, 8, 5), m.assign(0, 0, 8, 5))
	v2 := pair(t, tree.Affine(v1, 0, 4, 3, 1), m.affine(v1, 0, 4, 3, 1)) // fork A
	v3 := pair(t, tree.Affine(v1, 4, 8, 0, 7), m.affine(v1, 4, 8, 0, 7)) // fork B, same parent
	v4 := pair(t, tree.Assign(v2, 2, 6, 9), m.assign(v2, 2, 6, 9))       // grow fork A
	v5 := pair(t, tree.Affine(v3, 1, 7, 2, 2), m.affine(v3, 1, 7, 2, 2)) // grow fork B
	v6 := pair(t, tree.Affine(v4, 0, 8, 0, 0), m.affine(v4, 0, 8, 0, 0)) // zero out fork A

	for _, ver := range []int{0, v1, v2, v3, v4, v5, v6} {
		checkAllRanges(t, tree, m, ver, 8)
	}
	if got := tree.Sum(v6, 0, 8); got != 0 {
		t.Fatalf("fork A zeroed sum = %d, want 0", got)
	}
	if got := tree.Sum(v5, 0, 8); got == 0 {
		t.Fatalf("fork B must be unaffected by zeroing fork A")
	}
}

// TestZeroLengthRanges: empty updates still fork a version, empty sums are 0.
func TestZeroLengthRanges(t *testing.T) {
	const mod = 11
	tree := New(4, mod)
	m := newModel(4, mod)

	v1 := pair(t, tree.Assign(0, 2, 2, 5), m.assign(0, 2, 2, 5))         // no-op update
	v2 := pair(t, tree.Affine(v1, 0, 0, 3, 4), m.affine(v1, 0, 0, 3, 4)) // no-op update
	v3 := pair(t, tree.Assign(v2, 0, 4, 2), m.assign(v2, 0, 4, 2))

	if tree.Versions() != 4 {
		t.Fatalf("expected 4 versions, got %d", tree.Versions())
	}
	for _, ver := range []int{0, v1, v2, v3} {
		checkAllRanges(t, tree, m, ver, 4)
	}
	for l := 0; l <= 4; l++ {
		if got := tree.Sum(v3, l, l); got != 0 {
			t.Fatalf("empty sum at %d = %d, want 0", l, got)
		}
	}
}

// TestModOne: mod 1 collapses everything to zero.
func TestModOne(t *testing.T) {
	tree := New(3, 1)
	v1 := tree.Assign(0, 0, 3, 12345)
	v2 := tree.Affine(v1, 0, 3, 7, 9)
	for _, ver := range []int{0, v1, v2} {
		if got := tree.Sum(ver, 0, 3); got != 0 {
			t.Fatalf("mod 1 sum = %d, want 0", got)
		}
	}
}

// TestEmptyTree: n = 0 is legal and every operation is a no-op.
func TestEmptyTree(t *testing.T) {
	tree := New(0, 97)
	v1 := tree.Assign(0, 0, 0, 5)
	v2 := tree.Affine(v1, 0, 0, 2, 3)
	if got := tree.Sum(v2, 0, 0); got != 0 {
		t.Fatalf("sum = %d, want 0", got)
	}
	if tree.Versions() != 3 {
		t.Fatalf("expected 3 versions, got %d", tree.Versions())
	}
}

// TestRandomizedAgainstNaive hammers the tree with random forking updates,
// including negative parameters and zero-length ranges, and cross-checks
// every version against the naive model.
func TestRandomizedAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261004))
	for trial := 0; trial < 150; trial++ {
		n := rng.Intn(33)
		mod := int64(1 + rng.Intn(1000000))
		tree := New(n, mod)
		m := newModel(n, mod)
		for step := 0; step < 200; step++ {
			ver := rng.Intn(tree.Versions())
			l := rng.Intn(n + 1)
			r := rng.Intn(n + 1)
			if l > r {
				l, r = r, l
			}
			randArg := func() int64 {
				v := int64(rng.Intn(2*int(mod)+3)) - int64(mod) - 1
				return v
			}
			switch rng.Intn(3) {
			case 0:
				v := randArg()
				nv := tree.Assign(ver, l, r, v)
				mv := m.assign(ver, l, r, v)
				if nv != mv {
					t.Fatalf("version index mismatch: tree %d, model %d", nv, mv)
				}
			case 1:
				mul, add := randArg(), randArg()
				nv := tree.Affine(ver, l, r, mul, add)
				mv := m.affine(ver, l, r, mul, add)
				if nv != mv {
					t.Fatalf("version index mismatch: tree %d, model %d", nv, mv)
				}
			default:
				if got, want := tree.Sum(ver, l, r), m.sum(ver, l, r); got != want {
					t.Fatalf("trial %d step %d: version %d sum[%d,%d) mod %d: got %d, want %d",
						trial, step, ver, l, r, mod, got, want)
				}
			}
		}
		// Final exhaustive check of every version on small n.
		if n <= 8 {
			for ver := 0; ver < tree.Versions(); ver++ {
				checkAllRanges(t, tree, m, ver, n)
			}
		}
	}
}
