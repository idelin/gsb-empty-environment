package bwt

import (
	"bytes"
	"math/rand"
	"sort"
	"testing"
)

// naiveBWT is the reference implementation: it materializes all
// rotations, sorts them stably (equal rotations keep ascending start
// position) and reads off the last column and primary index. Only
// suitable for short inputs.
func naiveBWT(s []byte) ([]byte, int) {
	n := len(s)
	if n == 0 {
		return nil, 0
	}
	rots := make([]int, n)
	for i := range rots {
		rots[i] = i
	}
	sort.SliceStable(rots, func(a, b int) bool {
		i, j := rots[a], rots[b]
		for k := 0; k < n; k++ {
			x, y := s[(i+k)%n], s[(j+k)%n]
			if x != y {
				return x < y
			}
		}
		return false
	})
	last := make([]byte, n)
	primary := 0
	for r, start := range rots {
		if start == 0 {
			primary = r
		}
		last[r] = s[(start+n-1)%n]
	}
	return last, primary
}

func checkAgainstReference(t *testing.T, s []byte) {
	t.Helper()
	wantL, wantP := naiveBWT(s)
	gotL, gotP := Transform(s)
	if gotP != wantP || !bytes.Equal(gotL, wantL) {
		t.Fatalf("Transform(%q) = (%q, %d), want (%q, %d)", s, gotL, gotP, wantL, wantP)
	}
	inv, err := Inverse(gotL, gotP)
	if err != nil {
		t.Fatalf("Inverse(%q, %d): %v", gotL, gotP, err)
	}
	if !bytes.Equal(inv, s) {
		t.Fatalf("round trip of %q gave %q", s, inv)
	}
	if len(inv) != len(s) {
		t.Fatalf("inverse length %d, want %d", len(inv), len(s))
	}
}

// TestExhaustiveBinary enumerates every binary string up to length 12
// and compares against the rotation-materializing reference.
func TestExhaustiveBinary(t *testing.T) {
	for n := 0; n <= 12; n++ {
		for mask := 0; mask < 1<<uint(n); mask++ {
			s := make([]byte, n)
			for i := 0; i < n; i++ {
				if mask&(1<<uint(i)) != 0 {
					s[i] = 1
				}
			}
			checkAgainstReference(t, s)
		}
	}
}

// TestExhaustiveTernary enumerates every string over a 3-byte alphabet
// up to length 9.
func TestExhaustiveTernary(t *testing.T) {
	for n := 0; n <= 9; n++ {
		total := 1
		for i := 0; i < n; i++ {
			total *= 3
		}
		for v := 0; v < total; v++ {
			s := make([]byte, n)
			x := v
			for i := 0; i < n; i++ {
				s[i] = byte(x % 3)
				x /= 3
			}
			checkAgainstReference(t, s)
		}
	}
}

func TestRoundTripRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	sizes := []int{1, 2, 3, 7, 64, 255, 256, 257, 1000, 4096, 65536}
	for _, n := range sizes {
		s := make([]byte, n)
		rng.Read(s)
		last, primary := Transform(s)
		inv, err := Inverse(last, primary)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if !bytes.Equal(inv, s) {
			t.Fatalf("n=%d: round trip mismatch", n)
		}
		if len(inv) != n {
			t.Fatalf("n=%d: inverse length %d", n, len(inv))
		}
	}
}

func TestAllSameByte(t *testing.T) {
	for _, b := range []byte{0x00, 0x61, 0xFF} {
		s := bytes.Repeat([]byte{b}, 100000)
		last, primary := Transform(s)
		if !bytes.Equal(last, s) {
			t.Fatalf("byte %#x: last column is not the constant run", b)
		}
		inv, err := Inverse(last, primary)
		if err != nil {
			t.Fatalf("byte %#x: %v", b, err)
		}
		if !bytes.Equal(inv, s) {
			t.Fatalf("byte %#x: round trip mismatch", b)
		}
	}
}

// TestAllByteValues feeds data covering the full byte alphabet; no
// sentinel value is reserved anywhere.
func TestAllByteValues(t *testing.T) {
	var s []byte
	for r := 0; r < 4; r++ {
		for b := 0; b < 256; b++ {
			s = append(s, byte(b))
		}
	}
	last, primary := Transform(s)
	inv, err := Inverse(last, primary)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(inv, s) {
		t.Fatal("round trip mismatch over full byte alphabet")
	}
}

func TestEmptyInput(t *testing.T) {
	last, primary := Transform(nil)
	if primary != 0 || len(last) != 0 {
		t.Fatalf("Transform(nil) = (%v, %d)", last, primary)
	}
	inv, err := Inverse(nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv) != 0 {
		t.Fatalf("inverse of empty block has %d bytes", len(inv))
	}
}

func TestInversePrimaryOutOfRange(t *testing.T) {
	cases := []struct {
		last    []byte
		primary int
	}{
		{[]byte("abc"), 3},
		{[]byte("abc"), 99},
		{[]byte("abc"), -1},
		{nil, 1},
		{nil, -1},
	}
	for _, c := range cases {
		if _, err := Inverse(c.last, c.primary); err != ErrPrimaryIndex {
			t.Fatalf("Inverse(%q, %d) err = %v, want ErrPrimaryIndex", c.last, c.primary, err)
		}
	}
}

// TestLargeBlock exercises a 1 MiB block; the implementation sorts
// cyclic shifts in O(n log n) with O(n) memory and never builds
// rotation strings.
func TestLargeBlock(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	s := make([]byte, 1<<20)
	rng.Read(s)
	last, primary := Transform(s)
	inv, err := Inverse(last, primary)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(inv, s) {
		t.Fatal("1 MiB round trip mismatch")
	}
}

func TestLocate(t *testing.T) {
	text := bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog. "), 20)
	last, primary := Transform(text)
	idx, err := NewIndex(last, primary)
	if err != nil {
		t.Fatal(err)
	}
	n := len(text)
	patterns := [][]byte{
		[]byte("the"),
		[]byte("quick brown fox"),
		[]byte("dog. the"), // wraps across block boundary cyclically
		[]byte("xyz"),      // absent
	}
	for _, pat := range patterns {
		got := idx.Locate(pat)
		var want []int
		for pos := 0; pos < n; pos++ {
			match := true
			for k := 0; k < len(pat); k++ {
				if text[(pos+k)%n] != pat[k] {
					match = false
					break
				}
			}
			if match {
				want = append(want, pos)
			}
		}
		if len(pat) > n {
			continue
		}
		if len(got) != len(want) {
			t.Fatalf("Locate(%q) = %v, want %v", pat, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("Locate(%q) = %v, want %v", pat, got, want)
			}
		}
	}
}

func TestLocateOnAllSameByte(t *testing.T) {
	text := bytes.Repeat([]byte("a"), 5000)
	last, primary := Transform(text)
	idx, err := NewIndex(last, primary)
	if err != nil {
		t.Fatal(err)
	}
	got := idx.Locate([]byte("aaa"))
	if len(got) != 5000 {
		t.Fatalf("got %d positions, want 5000", len(got))
	}
	for i, p := range got {
		if p != i {
			t.Fatalf("position %d = %d", i, p)
		}
	}
}

func TestNewIndexRejectsBadPrimary(t *testing.T) {
	if _, err := NewIndex([]byte("ab"), 2); err != ErrPrimaryIndex {
		t.Fatalf("err = %v, want ErrPrimaryIndex", err)
	}
	if _, err := NewIndex(nil, 1); err != ErrPrimaryIndex {
		t.Fatalf("err = %v, want ErrPrimaryIndex", err)
	}
}
