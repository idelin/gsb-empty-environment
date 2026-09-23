package bitplane

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"
	"math/rand"
	"testing"
)

// expectedValue mirrors the decoder's output convention: unsigned streams
// zero-extend the low w bits, signed streams sign-extend them.
func expectedValue(v int64, w int, signed bool) int64 {
	x := uint64(v) & mask(w)
	if signed {
		return signExtend(x, w)
	}
	return int64(x)
}

func checkRoundtrip(t *testing.T, vals []int64, opts Options) Stats {
	t.Helper()
	w, err := opts.normalized()
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	buf, stats, err := Encode(vals, opts)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if stats.TotalBytes != len(buf) {
		t.Fatalf("stats total %d != buffer length %d", stats.TotalBytes, len(buf))
	}
	if stats.HeaderBytes+stats.IndexBytes+stats.DataBytes != stats.TotalBytes {
		t.Fatalf("stats parts %d+%d+%d != total %d",
			stats.HeaderBytes, stats.IndexBytes, stats.DataBytes, stats.TotalBytes)
	}
	got, err := Decode(buf, len(vals))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got) != len(vals) {
		t.Fatalf("decoded %d values, want %d", len(got), len(vals))
	}
	for i, v := range vals {
		if want := expectedValue(v, w, opts.Signed); got[i] != want {
			t.Fatalf("value %d: got %d, want %d", i, got[i], want)
		}
	}
	return stats
}

func TestRoundtripWidthsAndSigns(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for _, w := range []int{1, 2, 7, 8, 16, 31, 33, 63, 64} {
		for _, signed := range []bool{false, true} {
			vals := make([]int64, 500)
			for i := range vals {
				vals[i] = rng.Int63()
				if rng.Intn(2) == 0 {
					vals[i] = -vals[i] - 1
				}
			}
			checkRoundtrip(t, vals, Options{Width: w, Signed: signed})
		}
	}
}

func TestNegativeBitPatternPreserved(t *testing.T) {
	vals := []int64{0, -1, 1, -2, 2, math.MinInt64, math.MaxInt64, math.MinInt64 + 1, -123456789012345}
	checkRoundtrip(t, vals, Options{Width: 64, Signed: true})
	checkRoundtrip(t, vals, Options{Width: 64, Signed: false})
}

func TestSignExtremes(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	vals := make([]int64, 0, 1024)
	for i := 0; i < 512; i++ {
		vals = append(vals, math.MinInt64+int64(rng.Intn(3)))
		vals = append(vals, math.MaxInt64-int64(rng.Intn(3)))
	}
	checkRoundtrip(t, vals, Options{Width: 64, Signed: true})
	checkRoundtrip(t, vals, Options{Width: 64, Signed: false})
}

func TestAllZeroPlanesCompact(t *testing.T) {
	vals := make([]int64, 8192) // all zero: every plane is all-zero
	stats := checkRoundtrip(t, vals, Options{Width: 16})
	if stats.TotalBytes > 512 {
		t.Fatalf("all-zero stream took %d bytes, want compact (<= 512)", stats.TotalBytes)
	}
	if stats.ModeCounts[modeRLE]+stats.ModeCounts[modeDelta] == 0 {
		t.Fatalf("expected rle/delta plane chunks for zero planes, got %+v", stats.ModeCounts)
	}
	// Even values only: plane 0 is all zero, others literal-ish.
	vals = make([]int64, 4096)
	for i := range vals {
		vals[i] = int64(i * 2)
	}
	stats = checkRoundtrip(t, vals, Options{Width: 16})
	if stats.ModeCounts[modeRLE]+stats.ModeCounts[modeDelta] == 0 {
		t.Fatalf("expected compact zero plane, modes %+v", stats.ModeCounts)
	}
}

func TestTailBlockPreserved(t *testing.T) {
	// n=37 with block size 16 leaves a 5-value tail block that must survive.
	rng := rand.New(rand.NewSource(3))
	vals := make([]int64, 37)
	for i := range vals {
		vals[i] = rng.Int63n(1000) - 500
	}
	planes := buildPlanes(vals, 16, true)
	buf, stats, err := encodeWithBlockSize(planes, len(vals), 16, true, 16)
	if err != nil {
		t.Fatalf("encodeWithBlockSize: %v", err)
	}
	if stats.NumBlocks != 3 {
		t.Fatalf("numBlocks = %d, want 3 (16+16+5)", stats.NumBlocks)
	}
	got, err := Decode(buf, len(vals))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	for i, v := range vals {
		if want := expectedValue(v, 16, true); got[i] != want {
			t.Fatalf("value %d: got %d, want %d", i, got[i], want)
		}
	}
}

func TestExhaustiveBlockSizeOptimal(t *testing.T) {
	cases := []struct {
		n, w   int
		signed bool
		seed   int64
	}{
		{37, 6, true, 1},
		{50, 1, false, 2},
		{64, 12, true, 3},
		{100, 3, false, 4},
	}
	for _, tc := range cases {
		rng := rand.New(rand.NewSource(tc.seed))
		vals := make([]int64, tc.n)
		for i := range vals {
			vals[i] = rng.Int63n(200) - 100
		}
		buf, stats, err := Encode(vals, Options{Width: tc.w, Signed: tc.signed})
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		planes := buildPlanes(vals, tc.w, tc.signed)
		best := math.MaxInt
		for b := 1; b <= tc.n; b++ {
			if c := totalCost(planes, tc.n, tc.w, b); c < best {
				best = c
			}
		}
		if stats.TotalBytes != best {
			t.Fatalf("case %+v: encoder total %d, exhaustive optimum %d (block size %d)",
				tc, stats.TotalBytes, best, stats.BlockSize)
		}
		if len(buf) != best {
			t.Fatalf("case %+v: buffer %d bytes, optimum %d", tc, len(buf), best)
		}
	}
}

func TestRangeDecodeMatchesFull(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	vals := make([]int64, 1000)
	for i := range vals {
		vals[i] = rng.Int63n(1<<20) - 1<<19
	}
	buf, _, err := Encode(vals, Options{Width: 24, Signed: true})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	full, err := Decode(buf, len(vals))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	for trial := 0; trial < 50; trial++ {
		start := rng.Intn(len(vals))
		end := start + rng.Intn(len(vals)-start+1)
		got, err := DecodeRange(buf, start, end, end-start)
		if err != nil {
			t.Fatalf("DecodeRange(%d,%d): %v", start, end, err)
		}
		if len(got) != end-start {
			t.Fatalf("DecodeRange(%d,%d) returned %d values", start, end, len(got))
		}
		for i := range got {
			if got[i] != full[start+i] {
				t.Fatalf("range [%d,%d) value %d: got %d, want %d", start, end, i, got[i], full[start+i])
			}
		}
	}
}

// corruptBlock flips one byte inside the payload of block b.
func corruptBlock(t *testing.T, buf []byte, b int) {
	t.Helper()
	h, err := parseHeader(buf)
	if err != nil {
		t.Fatalf("parseHeader: %v", err)
	}
	idx := readIndex(buf, h.numBlocks)
	base := headerSize + h.numBlocks*indexEntrySize
	pos := base + int(idx[b].off) + int(idx[b].ln)/2
	buf[pos] ^= 0xFF
}

func TestCorruptBlockIsolation(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	vals := make([]int64, 1000)
	for i := range vals {
		vals[i] = rng.Int63n(1 << 30)
	}
	planes := buildPlanes(vals, 32, false)
	buf, stats, err := encodeWithBlockSize(planes, len(vals), 32, false, 128)
	if err != nil {
		t.Fatalf("encodeWithBlockSize: %v", err)
	}
	if stats.NumBlocks < 3 {
		t.Fatalf("need several blocks for isolation test, got %d", stats.NumBlocks)
	}
	last := stats.NumBlocks - 1
	corruptBlock(t, buf, last)

	// Full decode must fail and name the corrupted block.
	if _, err := Decode(buf, len(vals)); err == nil {
		t.Fatal("Decode of corrupted stream succeeded")
	} else if !contains(err.Error(), "block") {
		t.Fatalf("error does not identify block: %v", err)
	}

	// A range fully inside earlier blocks must still decode: unrelated
	// blocks are never expanded or checksum-verified.
	bs := stats.BlockSize
	got, err := DecodeRange(buf, 0, bs, bs)
	if err != nil {
		t.Fatalf("DecodeRange over healthy block failed: %v", err)
	}
	for i := 0; i < bs; i++ {
		if got[i] != vals[i] {
			t.Fatalf("value %d corrupted by unrelated block: got %d, want %d", i, got[i], vals[i])
		}
	}

	// A range overlapping the corrupted block must fail locally.
	if _, err := DecodeRange(buf, last*bs, int(stats.Values), bs); err == nil {
		t.Fatal("DecodeRange over corrupted block succeeded")
	}
}

func TestOutputQuota(t *testing.T) {
	vals := make([]int64, 100)
	buf, _, err := Encode(vals, Options{Width: 8})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if _, err := Decode(buf, 99); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("Decode with small quota: got %v, want ErrQuotaExceeded", err)
	}
	if _, err := Decode(buf, 100); err != nil {
		t.Fatalf("Decode with exact quota: %v", err)
	}
	if _, err := DecodeRange(buf, 0, 50, 49); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("DecodeRange over quota: got %v, want ErrQuotaExceeded", err)
	}

	// A header advertising a huge count must hit the quota, not the allocator.
	huge := make([]byte, len(buf))
	copy(huge, buf)
	binary.LittleEndian.PutUint64(huge[8:], 1<<40)
	if _, err := Decode(huge, 1<<20); err == nil {
		t.Fatal("Decode of huge-count stream succeeded")
	}
}

func TestHeaderValidation(t *testing.T) {
	vals := []int64{1, 2, 3, 4, 5}
	buf, _, err := Encode(vals, Options{Width: 8})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	fixCRC := func(b []byte) {
		binary.LittleEndian.PutUint32(b[40:], crc32.ChecksumIEEE(b[0:40]))
	}
	mutate := func(fn func([]byte)) []byte {
		b := make([]byte, len(buf))
		copy(b, buf)
		fn(b)
		return b
	}
	cases := map[string][]byte{
		"short":        buf[:20],
		"bad magic":    mutate(func(b []byte) { b[0] = 'X' }),
		"bad version":  mutate(func(b []byte) { b[4] = 99; fixCRC(b) }),
		"width zero":   mutate(func(b []byte) { b[6] = 0; fixCRC(b) }),
		"width 65":     mutate(func(b []byte) { b[6] = 65; fixCRC(b) }),
		"reserved set": mutate(func(b []byte) { b[7] = 1; fixCRC(b) }),
		"bad flags":    mutate(func(b []byte) { b[5] = 0x80; fixCRC(b) }),
		"bad crc":      mutate(func(b []byte) { b[40] ^= 1 }),
		"truncated":    buf[:len(buf)-1],
		"extra byte":   append(append([]byte{}, buf...), 0),
		"numBlocks+1":  mutate(func(b []byte) { binary.LittleEndian.PutUint32(b[20:], 2); fixCRC(b) }),
		"blockSize 0":  mutate(func(b []byte) { binary.LittleEndian.PutUint32(b[16:], 0); fixCRC(b) }),
	}
	for name, bad := range cases {
		if _, err := Decode(bad, 1000); err == nil {
			t.Fatalf("%s: decode succeeded on invalid stream", name)
		}
	}
}

func TestEmptyInput(t *testing.T) {
	buf, stats, err := Encode(nil, Options{Width: 8})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if stats.TotalBytes != headerSize {
		t.Fatalf("empty stream total %d, want header only %d", stats.TotalBytes, headerSize)
	}
	got, err := Decode(buf, 0)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("decoded %d values from empty stream", len(got))
	}
}

func TestInvalidWidth(t *testing.T) {
	if _, _, err := Encode([]int64{1}, Options{Width: 65}); err == nil {
		t.Fatal("Encode accepted width 65")
	}
	if _, _, err := Encode([]int64{1}, Options{Width: -3}); err == nil {
		t.Fatal("Encode accepted negative width")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
