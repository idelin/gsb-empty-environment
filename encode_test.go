package idset

import (
	"encoding/binary"
	"math/rand"
	"testing"
)

func blockHdr(hi uint16, typ byte, card uint32) []byte {
	b := make([]byte, 0, 7)
	b = binary.LittleEndian.AppendUint16(b, hi)
	b = append(b, typ)
	b = binary.LittleEndian.AppendUint32(b, card)
	return b
}

func encOf(payload ...[]byte) []byte {
	n := 4
	for _, p := range payload {
		n += len(p)
	}
	out := make([]byte, 0, n)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(payload)))
	for _, p := range payload {
		out = append(out, p...)
	}
	return out
}

func arrayPayload(vals ...uint16) []byte {
	b := blockHdr(0, byte(cArray), uint32(len(vals)))
	for _, v := range vals {
		b = binary.LittleEndian.AppendUint16(b, v)
	}
	return b
}

func runPayload(card uint32, ivs ...ival) []byte {
	b := blockHdr(0, byte(cRun), card)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(ivs)))
	for _, iv := range ivs {
		b = binary.LittleEndian.AppendUint16(b, iv.lo)
		b = binary.LittleEndian.AppendUint16(b, iv.hi)
	}
	return b
}

func bitmapPayload(card uint32, words map[int]uint64) []byte {
	b := blockHdr(0, byte(cBitmap), card)
	for w := 0; w < bitmapWords; w++ {
		b = binary.LittleEndian.AppendUint64(b, words[w])
	}
	return b
}

func TestDecodeValid(t *testing.T) {
	// Minimal canonical array block.
	s, err := Decode(encOf(arrayPayload(1, 2, 500)))
	if err != nil {
		t.Fatal(err)
	}
	if s.Size() != 3 || !s.Contains(500) {
		t.Fatal("decoded content wrong")
	}
	// Canonical single run covering a full block.
	s, err = Decode(encOf(runPayload(65536, ival{0, 65535})))
	if err != nil {
		t.Fatal(err)
	}
	if s.Size() != 65536 || s.Rank(65535) != 65536 {
		t.Fatal("full-block run decoded wrong")
	}
	// Canonical bitmap: 5000 alternating bits.
	src := New()
	for i := 0; i < 10000; i += 2 {
		src.Add(uint32(i))
	}
	enc := src.Encode()
	if _, err := Decode(enc); err != nil {
		t.Fatalf("bitmap block rejected: %v", err)
	}
}

func TestDecodeRejects(t *testing.T) {
	cases := []struct {
		name string
		enc  []byte
	}{
		{"empty input", nil},
		{"truncated header", []byte{1, 0}},
		{"array unsorted", encOf(arrayPayload(5, 3))},
		{"array duplicate", encOf(arrayPayload(5, 5))},
		{"array truncated", encOf(blockHdr(0, byte(cArray), 3), []byte{1, 0})},
		{"bitmap popcount mismatch", encOf(bitmapPayload(2, map[int]uint64{0: 0x1}))},
		{"bitmap truncated", encOf(blockHdr(0, byte(cBitmap), 1), make([]byte, 100))},
		{"runs overlapping", encOf(runPayload(8, ival{0, 5}, ival{4, 9}))},
		{"runs adjacent", encOf(runPayload(8, ival{0, 3}, ival{4, 7}))},
		{"run lo>hi", encOf(runPayload(1, ival{9, 3}))},
		{"run count mismatch", encOf(runPayload(5, ival{0, 3}))},
		{"run zero runs", encOf(runPayload(1))},
		{"card zero", encOf(blockHdr(0, byte(cArray), 0))},
		{"card too big", encOf(blockHdr(0, byte(cArray), 65537))},
		{"bad type tag", encOf(blockHdr(0, 9, 1), []byte{0, 0})},
		{"blocks out of order", encOf(
			append(blockHdr(7, byte(cArray), 1), 0, 0),
			append(blockHdr(3, byte(cArray), 1), 0, 0),
		)},
		{"duplicate block key", encOf(
			append(blockHdr(3, byte(cArray), 1), 0, 0),
			append(blockHdr(3, byte(cArray), 1), 1, 0),
		)},
		{"trailing garbage", append(encOf(arrayPayload(1)), 0xff)},
		{"non-canonical big array", encOf(arrayPayload(manyVals(5000)...))},
		{"non-canonical tiny bitmap", encOf(bitmapPayload(1, map[int]uint64{0: 0x1}))},
		{"non-canonical run", encOf(runPayload(2, ival{0, 0}, ival{5, 5}))}, // array(4B) < run(8B)
		{"non-canonical full bitmap", encOf(fullBitmapBlock())},
	}
	for _, tc := range cases {
		if _, err := Decode(tc.enc); err == nil {
			t.Errorf("%s: expected error, got none", tc.name)
		}
	}
}

func manyVals(n int) []uint16 {
	out := make([]uint16, n)
	for i := range out {
		out[i] = uint16(i * 12)
	}
	return out
}

// fullBitmapBlock encodes a full block as a bitmap; canonical form is a run.
func fullBitmapBlock() []byte {
	b := blockHdr(0, byte(cBitmap), 65536)
	for w := 0; w < bitmapWords; w++ {
		b = binary.LittleEndian.AppendUint64(b, ^uint64(0))
	}
	return b
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1234))
	sets := []*Set{New()}
	full := New()
	for i := 0; i < 65536; i++ {
		full.Add(uint32(i))
	}
	sets = append(sets, full)
	for i := 0; i < 10; i++ {
		sets = append(sets, setFromRef(randRef(rng, 1+rng.Intn(8000), 0.7)))
	}
	for i, s := range sets {
		enc := s.Encode()
		dec, err := Decode(enc)
		if err != nil {
			t.Fatalf("set %d: %v", i, err)
		}
		if dec.Size() != s.Size() {
			t.Fatalf("set %d: size %d != %d", i, dec.Size(), s.Size())
		}
		if enc2 := dec.Encode(); string(enc2) != string(enc) {
			t.Fatalf("set %d: re-encode differs", i)
		}
		// Content equality via rank probes.
		for j := 0; j < 50; j++ {
			p := rng.Uint32()
			if s.Rank(p) != dec.Rank(p) {
				t.Fatalf("set %d: rank(%d) differs", i, p)
			}
		}
	}
}

// TestEncodingSizes reports real serialized sizes for representative shapes.
func TestEncodingSizes(t *testing.T) {
	report := func(name string, s *Set) {
		t.Logf("%-34s %9d bytes  (values=%d, blocks=%d)", name, len(s.Encode()), s.Size(), len(s.keys))
	}
	report("empty", New())

	one := New()
	one.Add(42)
	report("single value", one)

	full := New()
	for i := 0; i < 65536; i++ {
		full.Add(uint32(i))
	}
	report("full block 0..65535 (run)", full)

	alt := New()
	for i := 0; i < 65536; i += 2 {
		alt.Add(uint32(i))
	}
	report("alternating full block (bitmap)", alt)

	sparse := New()
	rng := rand.New(rand.NewSource(5))
	for i := 0; i < 100; i++ {
		sparse.Add(uint32(rng.Intn(65536)))
	}
	report("100 sparse in one block (array)", sparse)

	half := New()
	for i := 0; i < 65536; i++ {
		if rng.Intn(2) == 0 {
			half.Add(uint32(i))
		}
	}
	report("~50% dense random block", half)

	wide := New()
	for i := 0; i < 1000000; i++ {
		wide.Add(rng.Uint32())
	}
	report("1M random over full domain", wide)

	clustered := New()
	for b := 0; b < 100; b++ {
		base := uint32(b*613+7) << 16
		for i := 0; i < 1000; i++ {
			clustered.Add(base | uint32(rng.Intn(2000)))
		}
	}
	report("100 blocks x ~1000 clustered", clustered)
}
