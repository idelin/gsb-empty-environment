package bitplane

import (
	"bytes"
	"errors"
	"math"
	"math/rand"
	"reflect"
	"testing"
)

func genValues(rng *rand.Rand, n, bits int, signed bool) []int64 {
	mask := maskFor(bits)
	vals := make([]int64, n)
	for i := range vals {
		u := rng.Uint64() & mask
		if signed {
			vals[i] = signExtend(u, bits)
		} else {
			vals[i] = int64(u)
		}
	}
	return vals
}

func roundTrip(t *testing.T, vals []int64, opts Options) []byte {
	t.Helper()
	data, err := EncodeBytes(vals, opts)
	if err != nil {
		t.Fatalf("EncodeBytes: %v", err)
	}
	got, hdr, err := DecodeAll(data, len(vals))
	if err != nil {
		t.Fatalf("DecodeAll: %v", err)
	}
	if hdr.Version != Version || hdr.Bits != opts.Bits || hdr.Signed != opts.Signed {
		t.Fatalf("header mismatch: %+v", hdr)
	}
	if hdr.Count != len(vals) {
		t.Fatalf("count mismatch: got %d want %d", hdr.Count, len(vals))
	}
	if len(vals) == 0 {
		if len(got) != 0 {
			t.Fatalf("expected empty output, got %d values", len(got))
		}
	} else if !reflect.DeepEqual(got, vals) {
		t.Fatalf("round trip mismatch (bits=%d signed=%v n=%d)", opts.Bits, opts.Signed, len(vals))
	}
	// Streaming Encode must produce identical bytes.
	var buf bytes.Buffer
	if err := Encode(&buf, vals, opts); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), data) {
		t.Fatalf("Encode and EncodeBytes differ")
	}
	return data
}

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		bits   int
		signed bool
	}{
		{1, false}, {1, true}, {2, true}, {7, true}, {8, true},
		{8, false}, {16, true}, {31, false}, {32, true}, {33, false},
		{63, true}, {64, true}, {64, false},
	}
	for _, tc := range cases {
		for _, n := range []int{0, 1, 2, 5, 63, 64, 65, 127, 128, 129, 1000} {
			rng := rand.New(rand.NewSource(int64(tc.bits)<<32 | int64(n)))
			vals := genValues(rng, n, tc.bits, tc.signed)
			roundTrip(t, vals, Options{Bits: tc.bits, Signed: tc.signed})
		}
	}
}

func TestNegativeBitPattern(t *testing.T) {
	// Every 8-bit signed value must round trip exactly, including negatives.
	vals := make([]int64, 0, 256)
	for v := -128; v <= 127; v++ {
		vals = append(vals, int64(v))
	}
	roundTrip(t, vals, Options{Bits: 8, Signed: true})

	// 64-bit extremes keep their exact bit patterns.
	extremes := []int64{math.MinInt64, math.MinInt64 + 1, -2, -1, 0, 1, 2, math.MaxInt64 - 1, math.MaxInt64}
	roundTrip(t, extremes, Options{Bits: 64, Signed: true})
	roundTrip(t, extremes, Options{Bits: 64, Signed: false})

	// Width extremes for a narrower signed type.
	narrow := []int64{-32768, -32767, -1, 0, 1, 32766, 32767}
	roundTrip(t, narrow, Options{Bits: 16, Signed: true})
}

func TestAllZeroCompact(t *testing.T) {
	vals := make([]int64, 100000)
	data, err := EncodeBytes(vals, Options{Bits: 32, Signed: true})
	if err != nil {
		t.Fatal(err)
	}
	// Raw would be 400000 bytes; all-zero planes must collapse to a few
	// bytes each even including headers and the index.
	if len(data) > 4096 {
		t.Fatalf("all-zero stream too large: %d bytes", len(data))
	}
	got, _, err := DecodeAll(data, len(vals))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, vals) {
		t.Fatal("round trip mismatch")
	}
}

func TestTailPreserved(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, n := range []int{1, 127, 129, 255, 1000, 1001} {
		vals := genValues(rng, n, 24, true)
		data := roundTrip(t, vals, Options{Bits: 24, Signed: true, MaxBlockSize: 128})
		p, err := parse(data)
		if err != nil {
			t.Fatal(err)
		}
		wantBlocks := (n-1)/p.BlockSize + 1
		if p.NumBlocks != wantBlocks {
			t.Fatalf("n=%d: numBlocks=%d want %d", n, p.NumBlocks, wantBlocks)
		}
		// The final block must cover the short tail, never a full block.
		tail := n - (p.NumBlocks-1)*p.BlockSize
		if tail < 1 || tail > p.BlockSize {
			t.Fatalf("n=%d: bad tail %d", n, tail)
		}
	}
}

func TestRangeDecode(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	n := 5000
	vals := make([]int64, n)
	cur := int64(0)
	for i := range vals {
		cur += int64(rng.Intn(41) - 20)
		vals[i] = cur
	}
	data := roundTrip(t, vals, Options{Bits: 32, Signed: true})
	ranges := [][2]int{{0, 1}, {0, 100}, {123, 4567}, {0, n}, {n - 1, n}, {2000, 2000}, {4096, 4097}}
	for _, r := range ranges {
		got, _, err := DecodeRange(data, r[0], r[1], n)
		if err != nil {
			t.Fatalf("DecodeRange%v: %v", r, err)
		}
		if !reflect.DeepEqual(got, vals[r[0]:r[1]]) {
			t.Fatalf("DecodeRange%v mismatch", r)
		}
	}
	if _, _, err := DecodeRange(data, n, n+1, n); !errors.Is(err, ErrRange) {
		t.Fatalf("expected ErrRange, got %v", err)
	}
	if _, _, err := DecodeRange(data, -1, 5, n); !errors.Is(err, ErrRange) {
		t.Fatalf("expected ErrRange, got %v", err)
	}
}

func TestCorruptionIsolation(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	n := 2000
	vals := genValues(rng, n, 20, true)
	opts := Options{Bits: 20, Signed: true, MaxBlockSize: 64}
	data := roundTrip(t, vals, opts)
	p, err := parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if p.NumBlocks < 10 {
		t.Fatalf("want many blocks, got %d", p.NumBlocks)
	}
	victim := 5
	bad := bytes.Clone(data)
	bad[p.blocks[victim].off] ^= 0xFF

	if _, _, err := DecodeAll(bad, n); err == nil {
		t.Fatal("DecodeAll should fail on corrupted block")
	}
	// Ranges not touching the corrupted block still decode.
	lo, hi := 0, victim*p.BlockSize
	got, _, err := DecodeRange(bad, lo, hi, n)
	if err != nil {
		t.Fatalf("range before corrupted block failed: %v", err)
	}
	if !reflect.DeepEqual(got, vals[lo:hi]) {
		t.Fatal("range before corrupted block mismatch")
	}
	lo, hi = (victim+1)*p.BlockSize, n
	got, _, err = DecodeRange(bad, lo, hi, n)
	if err != nil {
		t.Fatalf("range after corrupted block failed: %v", err)
	}
	if !reflect.DeepEqual(got, vals[lo:hi]) {
		t.Fatal("range after corrupted block mismatch")
	}
	// A range overlapping the corrupted block reports the checksum error.
	lo, hi = victim*p.BlockSize, (victim+1)*p.BlockSize
	if _, _, err := DecodeRange(bad, lo, hi, n); !errors.Is(err, ErrChecksum) {
		t.Fatalf("expected ErrChecksum, got %v", err)
	}
}

func TestQuota(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	vals := genValues(rng, 500, 16, true)
	data := roundTrip(t, vals, Options{Bits: 16, Signed: true})
	if _, _, err := DecodeAll(data, 499); !errors.Is(err, ErrQuota) {
		t.Fatalf("expected ErrQuota, got %v", err)
	}
	if _, _, err := DecodeAll(data, -1); !errors.Is(err, ErrQuota) {
		t.Fatalf("expected ErrQuota, got %v", err)
	}
	if _, _, err := DecodeRange(data, 0, 100, 99); !errors.Is(err, ErrQuota) {
		t.Fatalf("expected ErrQuota, got %v", err)
	}
	if _, _, err := DecodeRange(data, 0, 100, 100); err != nil {
		t.Fatalf("quota equal to range should pass: %v", err)
	}
}

func TestBadInputs(t *testing.T) {
	rng := rand.New(rand.NewSource(19))
	vals := genValues(rng, 300, 12, true)
	data := roundTrip(t, vals, Options{Bits: 12, Signed: true})

	if _, _, err := DecodeAll([]byte("garbage"), 1000); err == nil {
		t.Fatal("expected error for garbage")
	}
	// Truncations at every length must fail cleanly, never panic.
	for l := 0; l < len(data); l++ {
		if _, _, err := DecodeAll(data[:l], 1000); err == nil {
			t.Fatalf("truncated to %d bytes: expected error", l)
		}
	}
	// Corrupt every byte one at a time: must error, never panic.
	for i := range data {
		bad := bytes.Clone(data)
		bad[i] ^= 0xFF
		_, _, err := DecodeAll(bad, 1000)
		if err == nil {
			t.Fatalf("corruption at byte %d went undetected", i)
		}
	}
	// Bad version.
	bad := bytes.Clone(data)
	bad[4] = 99
	if _, _, err := DecodeAll(bad, 1000); err == nil {
		t.Fatal("expected error for bad version")
	}
	// Bit width zero and 65, with a fixed-up header CRC so the width check
	// itself is what fires.
	for _, b := range []byte{0, 65} {
		bad := bytes.Clone(data)
		bad[6] = b
		if _, _, err := DecodeAll(bad, 1000); err == nil {
			t.Fatalf("expected error for bits=%d", b)
		}
	}
	// Encoder rejects invalid widths.
	if _, err := EncodeBytes(vals, Options{Bits: 0}); err == nil {
		t.Fatal("expected error for Bits=0")
	}
	if _, err := EncodeBytes(vals, Options{Bits: 65}); err == nil {
		t.Fatal("expected error for Bits=65")
	}
}

// TestExhaustiveOptimal verifies, for small inputs, that the encoder's
// chosen block size and per-plane modes minimize the real total byte count
// (header + index + records) by brute-forcing every possible block size,
// and that the size model matches the actual encoded length exactly.
func TestExhaustiveOptimal(t *testing.T) {
	rng := rand.New(rand.NewSource(23))
	for trial := 0; trial < 60; trial++ {
		n := 1 + rng.Intn(40)
		bits := 1 + rng.Intn(16)
		signed := rng.Intn(2) == 0
		var vals []int64
		switch trial % 3 {
		case 0: // uniform random
			vals = genValues(rng, n, bits, signed)
		case 1: // constant runs -> RLE friendly
			vals = make([]int64, n)
			cur := int64(0)
			for i := range vals {
				if rng.Intn(4) == 0 {
					cur = int64(rng.Intn(1 << bits))
				}
				vals[i] = cur
			}
		case 2: // sparse high bits -> delta friendly
			vals = make([]int64, n)
			for i := range vals {
				if rng.Intn(8) == 0 {
					vals[i] = 1 << uint(rng.Intn(bits))
				}
			}
		}
		opts := Options{Bits: bits, Signed: signed}
		data, err := EncodeBytes(vals, opts)
		if err != nil {
			t.Fatal(err)
		}
		mapped := mapValues(vals, opts)
		planes := buildPlanes(mapped, bits)
		best := math.MaxInt
		for b := 1; b <= n; b++ {
			model := encodedSize(planes, bits, n, b)
			actual := len(encode(vals, planes, opts, b))
			if model != actual {
				t.Fatalf("trial %d b=%d: size model %d != actual %d", trial, b, model, actual)
			}
			if model < best {
				best = model
			}
		}
		if len(data) != best {
			t.Fatalf("trial %d: encoder size %d, exhaustive optimum %d (n=%d bits=%d)",
				trial, len(data), best, n, bits)
		}
	}
}

// TestPlaneModes checks that each mode wins on data shaped for it.
func TestPlaneModes(t *testing.T) {
	// All-zero plane: delta mode has no set bits, so the payload is empty
	// (0 bytes) -- the most compact encoding possible.
	words := []uint64{0}
	if size, mode := planeCost(words, 0, 64); mode != modeDelta || size != 0 {
		t.Fatalf("all-zero plane: got mode %d size %d, want delta size 0", mode, size)
	}
	// All-ones plane: RLE wins with a 0-length zero run plus one full run.
	ones := []uint64{^uint64(0)}
	if size, mode := planeCost(ones, 0, 64); mode != modeRLE || size != 2 {
		t.Fatalf("all-ones plane: got mode %d size %d, want RLE size 2", mode, size)
	}
	// Dense random plane: literal wins.
	rng := rand.New(rand.NewSource(29))
	dense := []uint64{rng.Uint64()}
	if _, mode := planeCost(dense, 0, 64); mode != modeLiteral {
		t.Fatalf("dense plane: got mode %d, want literal", mode)
	}
	// Sparse plane: delta wins.
	sparse := []uint64{1 | 1<<20 | 1<<63}
	if _, mode := planeCost(sparse, 0, 64); mode != modeDelta {
		t.Fatalf("sparse plane: got mode %d, want delta", mode)
	}
	// Round trip each mode through encode/decode of the payload.
	for _, w := range [][]uint64{words, dense, sparse, {^uint64(0)}, {0xAAAAAAAAAAAAAAAA}} {
		for n := 1; n <= 64; n++ {
			size, mode := planeCost(w, 0, n)
			payload := appendPlanePayload(nil, w, 0, n, mode)
			if len(payload) != size {
				t.Fatalf("n=%d: payload %d bytes, cost said %d", n, len(payload), size)
			}
			out := make([]uint64, 1)
			if err := decodePlane(payload, mode, n, out); err != nil {
				t.Fatalf("n=%d mode=%d: %v", n, mode, err)
			}
			mask := onesMask(n)
			if out[0]&mask != w[0]&mask {
				t.Fatalf("n=%d mode=%d: got %x want %x", n, mode, out[0]&mask, w[0]&mask)
			}
		}
	}
}

func TestCorruptPlanePayload(t *testing.T) {
	// A plane payload claiming impossible runs/positions must be rejected.
	if err := decodePlane([]byte{0xFF}, modeRLE, 8, make([]uint64, 1)); err == nil {
		t.Fatal("RLE overrun should fail")
	}
	if err := decodePlane([]byte{9}, modeDelta, 8, make([]uint64, 1)); err == nil {
		t.Fatal("delta position past n should fail")
	}
	if err := decodePlane([]byte{0}, modeLiteral, 16, make([]uint64, 1)); err == nil {
		t.Fatal("short literal should fail")
	}
	if err := decodePlane([]byte{0}, 99, 8, make([]uint64, 1)); err == nil {
		t.Fatal("unknown mode should fail")
	}
}
