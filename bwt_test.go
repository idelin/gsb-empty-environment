package bwt

import (
	"bytes"
	"errors"
	"math/rand"
	"sort"
	"testing"
)

// referenceEncode builds all n full rotation strings and sorts them stably
// (ties keep original start order). Only used for short inputs in tests.
func referenceEncode(s []byte) ([]byte, int) {
	n := len(s)
	if n == 0 {
		return nil, 0
	}
	type rotation struct {
		start int
		text  string
	}
	rots := make([]rotation, n)
	for i := 0; i < n; i++ {
		r := make([]byte, 0, n)
		r = append(r, s[i:]...)
		r = append(r, s[:i]...)
		rots[i] = rotation{start: i, text: string(r)}
	}
	sort.SliceStable(rots, func(a, b int) bool { return rots[a].text < rots[b].text })
	out := make([]byte, n)
	primary := -1
	for i, r := range rots {
		out[i] = r.text[n-1]
		if r.start == 0 {
			primary = i
		}
	}
	return out, primary
}

func roundTrip(t *testing.T, src []byte) {
	t.Helper()
	enc, primary := Encode(src)
	dec, err := Decode(enc, primary)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(dec) != len(src) {
		t.Fatalf("decoded length %d, want %d", len(dec), len(src))
	}
	if !bytes.Equal(dec, src) {
		t.Fatalf("round trip mismatch (len %d)", len(src))
	}
}

// TestExhaustiveShort compares Encode against the explicit-rotation
// reference for every string over {0,1,2} up to length 6, and verifies
// the inverse for each.
func TestExhaustiveShort(t *testing.T) {
	const maxLen = 6
	total := 0
	var walk func(s []byte)
	walk = func(s []byte) {
		enc, primary := Encode(s)
		refEnc, refPrimary := referenceEncode(s)
		if primary != refPrimary || !bytes.Equal(enc, refEnc) {
			t.Fatalf("Encode(%v) = (%v, %d), reference = (%v, %d)",
				s, enc, primary, refEnc, refPrimary)
		}
		roundTrip(t, s)
		total++
		if len(s) < maxLen {
			for b := byte(0); b < 3; b++ {
				walk(append(append([]byte{}, s...), b))
			}
		}
	}
	walk(nil)
	t.Logf("verified %d strings", total)
}

func TestEmptyBlock(t *testing.T) {
	enc, primary := Encode(nil)
	if len(enc) != 0 || primary != 0 {
		t.Fatalf("Encode(nil) = (%v, %d)", enc, primary)
	}
	dec, err := Decode(nil, 0)
	if err != nil || len(dec) != 0 {
		t.Fatalf("Decode(nil, 0) = (%v, %v)", dec, err)
	}
	if _, err := Decode(nil, 1); !errors.Is(err, ErrPrimaryIndex) {
		t.Fatalf("Decode(nil, 1) err = %v", err)
	}
	var buf bytes.Buffer
	if err := Write(&buf, nil); err != nil {
		t.Fatal(err)
	}
	out, err := Read(&buf, 0)
	if err != nil || len(out) != 0 {
		t.Fatalf("Read empty = (%v, %v)", out, err)
	}
}

func TestAllSameByte(t *testing.T) {
	for _, n := range []int{1, 2, 7, 1000, 1 << 16} {
		src := bytes.Repeat([]byte{0xAB}, n)
		enc, primary := Encode(src)
		if !bytes.Equal(enc, src) {
			t.Fatalf("n=%d: all-same block must transform to itself", n)
		}
		roundTrip(t, src)
		_ = primary
	}
}

func TestFullByteAlphabet(t *testing.T) {
	src := make([]byte, 0, 256*4)
	for i := 0; i < 4; i++ {
		for b := 0; b < 256; b++ {
			src = append(src, byte(b))
		}
	}
	roundTrip(t, src)
}

func TestRoundTripRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for _, n := range []int{1, 2, 3, 255, 256, 257, 4096, 1 << 20} {
		src := make([]byte, n)
		rng.Read(src)
		roundTrip(t, src)
	}
}

func TestPeriodicBlocks(t *testing.T) {
	for _, src := range [][]byte{
		[]byte("ababababab"),
		[]byte("abcabcabcabc"),
		bytes.Repeat([]byte("xy"), 500),
	} {
		enc, primary := Encode(src)
		refEnc, refPrimary := referenceEncode(src)
		if primary != refPrimary || !bytes.Equal(enc, refEnc) {
			t.Fatalf("Encode(%q) disagrees with reference", src)
		}
		roundTrip(t, src)
	}
}

func TestPrimaryIndexOutOfRange(t *testing.T) {
	enc, _ := Encode([]byte("banana"))
	for _, p := range []int{-1, len(enc), len(enc) + 100} {
		if _, err := Decode(enc, p); !errors.Is(err, ErrPrimaryIndex) {
			t.Fatalf("Decode(primary=%d) err = %v", p, err)
		}
		if _, err := NewIndex(enc, p); !errors.Is(err, ErrPrimaryIndex) {
			t.Fatalf("NewIndex(primary=%d) err = %v", p, err)
		}
	}
	// Corrupt the primary index in a container.
	var buf bytes.Buffer
	if err := Write(&buf, []byte("banana")); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	raw[16] = byte(len(enc)) // primary = n, out of range
	if _, err := Read(bytes.NewReader(raw), 1<<20); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Read with bad primary err = %v", err)
	}
}

func encodeContainer(t *testing.T, src []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, src); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestCorruptionDetection(t *testing.T) {
	src := []byte("the quick brown fox jumps over the lazy dog")
	good := encodeContainer(t, src)

	t.Run("counts", func(t *testing.T) {
		raw := bytes.Clone(good)
		raw[28]++ // first count entry
		if _, err := Read(bytes.NewReader(raw), 1<<20); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("counts-swapped", func(t *testing.T) {
		// Keep the sum equal but break the histogram vs payload:
		// swap the counts of 'e' and 't' (both present in src).
		raw := bytes.Clone(good)
		e, t_ := 28+int('e')*4, 28+int('t')*4
		raw[e], raw[t_] = raw[t_], raw[e]
		if _, err := Read(bytes.NewReader(raw), 1<<20); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("checksum", func(t *testing.T) {
		raw := bytes.Clone(good)
		raw[24] ^= 0xFF
		if _, err := Read(bytes.NewReader(raw), 1<<20); !errors.Is(err, ErrChecksum) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("payload", func(t *testing.T) {
		raw := bytes.Clone(good)
		raw[headerSize] ^= 0xFF // breaks counts check and/or CRC
		if _, err := Read(bytes.NewReader(raw), 1<<20); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("magic", func(t *testing.T) {
		raw := bytes.Clone(good)
		raw[0] = 'X'
		if _, err := Read(bytes.NewReader(raw), 1<<20); !errors.Is(err, ErrBadMagic) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("version", func(t *testing.T) {
		raw := bytes.Clone(good)
		raw[4] = 99
		if _, err := Read(bytes.NewReader(raw), 1<<20); !errors.Is(err, ErrUnsupportedVersion) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("truncated", func(t *testing.T) {
		if _, err := Read(bytes.NewReader(good[:len(good)-3]), 1<<20); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestBoundedOutput(t *testing.T) {
	src := make([]byte, 1000)
	rand.New(rand.NewSource(1)).Read(src)
	raw := encodeContainer(t, src)
	if _, err := Read(bytes.NewReader(raw), 999); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}
	out, err := Read(bytes.NewReader(raw), 1000)
	if err != nil || !bytes.Equal(out, src) {
		t.Fatalf("Read at exact budget: %v", err)
	}
}

func TestContainerRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, n := range []int{0, 1, 100, 1 << 18} {
		src := make([]byte, n)
		rng.Read(src)
		raw := encodeContainer(t, src)
		out, err := Read(bytes.NewReader(raw), 1<<30)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if !bytes.Equal(out, src) {
			t.Fatalf("n=%d: container round trip mismatch", n)
		}
	}
}

func TestIndexInverseAndLF(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	src := make([]byte, 5000)
	rng.Read(src)
	enc, primary := Encode(src)
	x, err := NewIndex(enc, primary)
	if err != nil {
		t.Fatal(err)
	}
	if got := x.Inverse(); !bytes.Equal(got, src) {
		t.Fatal("Index.Inverse mismatch")
	}
	// Walking LF from the primary index n times must return to the start.
	row := primary
	for i := 0; i < len(enc); i++ {
		row = x.LF(row)
	}
	if row != primary {
		t.Fatalf("LF cycle did not close: got row %d", row)
	}
}

func naiveLocate(src, pat []byte) []int {
	var out []int
	for i := 0; i+len(pat) <= len(src); i++ {
		if bytes.Equal(src[i:i+len(pat)], pat) {
			out = append(out, i)
		}
	}
	return out
}

func TestLocate(t *testing.T) {
	src := []byte("the quick brown fox jumps over the lazy dog, the end")
	enc, primary := Encode(src)
	x, err := NewIndex(enc, primary)
	if err != nil {
		t.Fatal(err)
	}
	if got := x.Locate(nil); got != nil {
		t.Fatalf("Locate(nil) = %v, want nil", got)
	}
	for _, pat := range [][]byte{
		[]byte("the"), []byte("quick"), []byte("z"), []byte("the "),
		[]byte("not present"), src,
	} {
		got := x.Locate(pat)
		want := naiveLocate(src, pat)
		if len(want) == 0 {
			want = nil
		}
		if len(got) == 0 {
			got = nil
		}
		if !equalInts(got, want) {
			t.Fatalf("Locate(%q) = %v, want %v", pat, got, want)
		}
	}
}

func TestLocateRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	src := make([]byte, 4096)
	for i := range src {
		src[i] = byte(rng.Intn(4)) // small alphabet: many matches
	}
	enc, primary := Encode(src)
	x, err := NewIndex(enc, primary)
	if err != nil {
		t.Fatal(err)
	}
	for k := 0; k < 50; k++ {
		m := 1 + rng.Intn(8)
		pat := make([]byte, m)
		for i := range pat {
			pat[i] = byte(rng.Intn(4))
		}
		got := x.Locate(pat)
		want := naiveLocate(src, pat)
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !equalInts(got, want) {
			t.Fatalf("Locate(%v) = %v, want %v", pat, got, want)
		}
	}
}

func equalInts(a, b []int) bool {
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
