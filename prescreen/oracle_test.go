package prescreen

import (
	"crypto/ed25519"
	"math/rand"
	"testing"
)

// enumerate expands an affine set into concrete block vectors. It is used only
// by the 6-bit reference tests; production admission never calls anything like
// this and is proven to stay algebraic on the 48-bit tests instead.
func enumerate(af Affine) []Vec {
	if af.Empty {
		return nil
	}
	pts := []Vec{af.Offset}
	for _, d := range af.Dir {
		add := make([]Vec, 0, len(pts))
		for _, p := range pts {
			add = append(add, p^d)
		}
		pts = append(pts, add...)
	}
	return pts
}

// oracleUnionSize is the brute-force union cardinality over enumerated sets.
func oracleUnionSize(sets []Affine) uint64 {
	seen := map[Vec]struct{}{}
	for _, af := range sets {
		for _, p := range enumerate(af) {
			seen[p] = struct{}{}
		}
	}
	return uint64(len(seen))
}

// oracleApply enumerates the exact image set of af under m, then re-wraps the
// resulting finite point set as an affine check (the image of an affine set
// under an affine map is itself affine).
func oracleApply(m AffineMap, af Affine) map[Vec]struct{} {
	out := map[Vec]struct{}{}
	for _, p := range enumerate(af) {
		out[m.linear(p)^m.Bias] = struct{}{}
	}
	return out
}

func pointSet(af Affine) map[Vec]struct{} {
	out := map[Vec]struct{}{}
	for _, p := range enumerate(af) {
		out[p] = struct{}{}
	}
	return out
}

func samePointSet(a, b map[Vec]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; ok != true {
			return false
		}
	}
	return true
}

// testKeys holds the Ed25519 keys used across the test harness.
type testKeys struct {
	root     ed25519.PrivateKey
	rootPub  ed25519.PublicKey
	other    ed25519.PrivateKey
	otherPub ed25519.PublicKey
	subj     ed25519.PrivateKey
	subjPub  ed25519.PublicKey
}

func newTestKeys(t *testing.T) testKeys {
	t.Helper()
	rootPub, root, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	otherPub, other, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	subjPub, subj, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return testKeys{
		root: root, rootPub: rootPub,
		other: other, otherPub: otherPub,
		subj: subj, subjPub: subjPub,
	}
}

func policyWith(roots []ed25519.PublicKey, manifests ...ViewManifest) Policy {
	return Policy{Roots: roots, Manifests: manifests}
}

// randAffine6 builds a random affine subspace inside the low 6 bits.
func randAffine6(rng *rand.Rand) Affine {
	dim := rng.Intn(7)
	dir := make([]Vec, 0, dim)
	used := map[Vec]bool{}
	for len(dir) < dim {
		v := Vec(rng.Intn(1 << 6))
		if v == 0 || used[v] {
			continue
		}
		used[v] = true
		dir = append(dir, v)
	}
	return NewAffine(dir, Vec(rng.Intn(1<<6)))
}

// randMap6 builds a random affine map operating on the low 6 bits.
func randMap6(rng *rand.Rand) AffineMap {
	var rows [Width]Vec
	for i := 0; i < 6; i++ {
		rows[i] = Vec(rng.Intn(1 << 6))
	}
	return AffineMap{Rows: rows, Bias: Vec(rng.Intn(1 << 6))}
}
