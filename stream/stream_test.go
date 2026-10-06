package stream

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

// TestFixedRecurrence locks the public SplitMix64 recurrence to golden
// values so the specification can never drift silently.
func TestFixedRecurrence(t *testing.T) {
	want := []uint64{
		0xE220A8397B1DCDAF,
		0x6E789E6AA1B965F4,
		0x06C45D188009454F,
		0xF88BB8A8724C81EC,
		0x1B39896A51A8749B,
	}
	got := seq(New(0), len(want))
	if !equal(got, want) {
		t.Fatalf("recurrence mismatch: got %x want %x", got, want)
	}
}

// TestSamePathSameStream verifies identical master seed and path give
// identical sequences.
func TestSamePathSameStream(t *testing.T) {
	a := New(42).Child("ingest").Child("shard-7")
	b := New(42).Child("ingest").Child("shard-7")
	if !equal(seq(a, 64), seq(b, 64)) {
		t.Fatal("same seed and path produced different streams")
	}
}

// TestDerivationOrderIrrelevant derives siblings in swapped orders and
// confirms each child is bit-identical regardless of when it was derived.
func TestDerivationOrderIrrelevant(t *testing.T) {
	const seed = 20261006

	// Order 1: derive a, then b, then advance the parent, then derive c.
	p1 := New(seed)
	a1 := p1.Child("a")
	b1 := p1.Child("b")
	p1.Uint64()
	p1.Uint64()
	c1 := p1.Child("c")

	// Order 2: swapped — derive b, then a, then advance identically, then c.
	p2 := New(seed)
	b2 := p2.Child("b")
	a2 := p2.Child("a")
	p2.Uint64()
	p2.Uint64()
	c2 := p2.Child("c")

	if !equal(seq(a1, 32), seq(a2, 32)) {
		t.Fatal("child \"a\" differs across derivation orders")
	}
	if !equal(seq(b1, 32), seq(b2, 32)) {
		t.Fatal("child \"b\" differs across derivation orders")
	}
	if !equal(seq(c1, 32), seq(c2, 32)) {
		t.Fatal("child \"c\" differs across derivation orders")
	}
	if equal(seq(c1, 32), seq(New(seed).Child("c"), 32)) {
		t.Fatal("children derived from different parent states must differ")
	}
	// Deriving must never advance or mutate the parent.
	p3 := New(seed)
	before := seq(p3, 8)
	p4 := New(seed)
	p4.Child("distraction")
	p4.Child("another")
	if after := seq(p4, 8); !equal(before, after) {
		t.Fatal("Child mutated its parent")
	}
}

// TestSiblingIndependence advances one child heavily and confirms its
// sibling's sequence is untouched.
func TestSiblingIndependence(t *testing.T) {
	root := New(7)
	left := root.Child("left")
	right := root.Child("right")

	want := seq(New(7).Child("right"), 64) // independent reference
	seq(left, 100000)                      // burn the sibling
	root.Uint64()                          // and the parent
	got := seq(right, 64)
	if !equal(want, got) {
		t.Fatal("advancing a sibling or parent changed this stream")
	}
}

// TestPathEncodingUnambiguous ensures different segmentations and
// separator-like bytes cannot collide.
func TestPathEncodingUnambiguous(t *testing.T) {
	root := New(1)
	pairs := [][2]*Stream{
		{root.Child("ab").Child("c"), root.Child("a").Child("bc")},
		{root.Child("a/b"), root.Child("a").Child("b")},
		{root.Child("a\x00b"), root.Child("a").Child("b")},
		{root.Child(""), root.Child("\x00")},
		{root.Child("ab"), root.Child("a").Child("b")},
	}
	for i, p := range pairs {
		if equal(seq(p[0], 16), seq(p[1], 16)) {
			t.Fatalf("pair %d: ambiguous path encoding produced identical streams", i)
		}
	}
}

// TestSaveRestore saves mid-stream and confirms the resumed sequence is
// bit-identical to the uninterrupted one.
func TestSaveRestore(t *testing.T) {
	full := New(99).Child("task")
	head := seq(full, 50)
	saved := full.Save()
	tail := seq(full, 200)

	resumed, err := Restore(saved)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := seq(resumed, 200); !equal(got, tail) {
		t.Fatal("restored stream diverged from original")
	}
	// Re-deriving a child from the restored stream must match too.
	if !equal(seq(full.Child("sub"), 16), seq(resumed.Child("sub"), 16)) {
		t.Fatal("child derivation diverged after restore")
	}
	_ = head
}

// TestRestoreRejectsBadData covers length, magic, and version checks.
func TestRestoreRejectsBadData(t *testing.T) {
	good := New(3).Save()
	if _, err := Restore(good[:len(good)-1]); err == nil {
		t.Fatal("short data accepted")
	}
	bad := append([]byte(nil), good...)
	bad[0] = 'X'
	if _, err := Restore(bad); err == nil {
		t.Fatal("bad magic accepted")
	}
	bad = append([]byte(nil), good...)
	bad[4] = saveVersion + 1
	if _, err := Restore(bad); err == nil {
		t.Fatal("unsupported version accepted")
	}
}

// TestByteEncoding locks the little-endian byte encoding of Uint64.
func TestByteEncoding(t *testing.T) {
	s := New(0)
	buf := make([]byte, 8)
	if _, err := s.Read(buf); err != nil {
		t.Fatal(err)
	}
	want := []byte{0xAF, 0xCD, 0x1D, 0x7B, 0x39, 0xA8, 0x20, 0xE2} // 0xE220A8397B1DCDAF LE
	if !bytes.Equal(buf, want) {
		t.Fatalf("byte encoding: got %x want %x", buf, want)
	}
	// Odd sizes slice the final word.
	s2 := New(0)
	odd := make([]byte, 11)
	s2.Read(odd)
	s3 := New(0)
	full := make([]byte, 16)
	s3.Read(full)
	if !bytes.Equal(odd, full[:11]) {
		t.Fatal("odd-length read is not a prefix of the word stream")
	}
}
