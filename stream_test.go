package streamrand

import (
	"bytes"
	"testing"
)

func seq(s *Stream, n int) []uint64 {
	out := make([]uint64, n)
	for i := range out {
		out[i] = s.Uint64()
	}
	return out
}

func equal(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Locks the fixed public recurrence (SplitMix64) for master seed 42.
func TestGoldenRecurrence(t *testing.T) {
	want := []uint64{
		13679457532755275413,
		2949826092126892291,
		5139283748462763858,
		6349198060258255764,
		701532786141963250,
	}
	if got := seq(New(42), 5); !equal(got, want) {
		t.Fatalf("recurrence mismatch: got %v want %v", got, want)
	}
}

// Locks the fixed public derivation function for path ("train","epoch-3").
func TestGoldenDerived(t *testing.T) {
	want := []uint64{
		5507751887704037974,
		5804378252372249265,
		7178253813293084222,
	}
	got := seq(New(42).Derive("train", "epoch-3"), 3)
	if !equal(got, want) {
		t.Fatalf("derivation mismatch: got %v want %v", got, want)
	}
}

// Deriving siblings in swapped order must yield identical streams.
func TestDerivationOrderIndependent(t *testing.T) {
	const seed = 7

	p1 := New(seed)
	a1 := p1.Derive("alpha")
	b1 := p1.Derive("beta")
	c1 := p1.Derive("gamma")

	p2 := New(seed)
	c2 := p2.Derive("gamma")
	b2 := p2.Derive("beta")
	a2 := p2.Derive("alpha")

	if !equal(seq(a1, 16), seq(a2, 16)) {
		t.Fatal("stream alpha differs across derivation orders")
	}
	if !equal(seq(b1, 16), seq(b2, 16)) {
		t.Fatal("stream beta differs across derivation orders")
	}
	if !equal(seq(c1, 16), seq(c2, 16)) {
		t.Fatal("stream gamma differs across derivation orders")
	}
}

// Multi-segment derivation must compose: Derive(a,b) == Derive(a).Derive(b).
func TestDerivePathComposition(t *testing.T) {
	direct := New(99).Derive("task", "shard-01", "worker-7")
	stepped := New(99).Derive("task").Derive("shard-01").Derive("worker-7")
	if !equal(seq(direct, 16), seq(stepped, 16)) {
		t.Fatal("composed path derivation mismatch")
	}
}

// Advancing one stream must not affect its parent or siblings.
func TestSiblingIndependence(t *testing.T) {
	parent := New(1234)
	left := parent.Derive("left")
	right := parent.Derive("right")

	wantRight := seq(New(1234).Derive("right"), 8)
	wantParent := seq(New(1234), 8)

	seq(left, 1000) // heavy advancement of one child

	if got := seq(right, 8); !equal(got, wantRight) {
		t.Fatal("sibling stream changed after advancing another child")
	}
	if got := seq(parent, 8); !equal(got, wantParent) {
		t.Fatal("parent stream changed by derivation or child advancement")
	}
}

// Length-prefixed segment encoding must be unambiguous.
func TestUnambiguousEncoding(t *testing.T) {
	if bytes.Equal(EncodePath("ab", "c"), EncodePath("a", "bc")) {
		t.Fatal("encoding collides for (ab,c) and (a,bc)")
	}
	if bytes.Equal(EncodePath("a", ""), EncodePath("", "a")) {
		t.Fatal("encoding collides for (a,\"\") and (\"\",a)")
	}
	if bytes.Equal(EncodePath("ab"), EncodePath("a", "b")) {
		t.Fatal("encoding collides for (ab) and (a,b)")
	}
	s1 := New(5).Derive("ab", "c")
	s2 := New(5).Derive("a", "bc")
	if equal(seq(s1, 8), seq(s2, 8)) {
		t.Fatal("derived streams collide for ambiguous paths")
	}
}

// Saved state (with version) must restore the exact stream position.
func TestSaveRestore(t *testing.T) {
	s := New(2024).Derive("job", "phase-2")
	seq(s, 37) // advance to a non-trivial position

	data, err := s.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := seq(s, 16)

	var r Stream
	if err := r.UnmarshalBinary(data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := seq(&r, 16); !equal(got, want) {
		t.Fatal("restored stream diverges from original")
	}
}

// Unknown versions and malformed data must be rejected.
func TestRestoreRejectsBadState(t *testing.T) {
	data, err := New(1).MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var s Stream
	if err := s.UnmarshalBinary(data[:8]); err != ErrBadState {
		t.Fatalf("short data: got %v", err)
	}

	badVersion := append([]byte(nil), data...)
	badVersion[7] = 0xFF
	if err := s.UnmarshalBinary(badVersion); err != ErrBadState {
		t.Fatalf("bad version: got %v", err)
	}

	badMagic := append([]byte(nil), data...)
	badMagic[0] ^= 0xFF
	if err := s.UnmarshalBinary(badMagic); err != ErrBadState {
		t.Fatalf("bad magic: got %v", err)
	}
}

// Float64 stays in [0,1) and Read fills deterministically.
func TestDerivedHelpers(t *testing.T) {
	s := New(9)
	for i := 0; i < 1000; i++ {
		if f := s.Float64(); f < 0 || f >= 1 {
			t.Fatalf("Float64 out of range: %v", f)
		}
	}
	b1 := make([]byte, 100)
	b2 := make([]byte, 100)
	if _, err := New(9).Derive("io").Read(b1); err != nil {
		t.Fatal(err)
	}
	if _, err := New(9).Derive("io").Read(b2); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatal("Read not deterministic")
	}
}
