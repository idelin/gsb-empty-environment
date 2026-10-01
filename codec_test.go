package bitpack

import (
	"math"
	"math/rand"
	"reflect"
	"testing"
)

func roundTrip(t *testing.T, vals []int64, blockSize int) *Reader {
	t.Helper()
	data := Encode(vals, blockSize)
	r, err := Open(data)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := r.DecodeAll()
	if err != nil {
		t.Fatalf("DecodeAll: %v", err)
	}
	// Independent comparison against the original integer list.
	if len(got) == 0 && len(vals) == 0 {
		return r
	}
	if !reflect.DeepEqual(got, vals) {
		t.Fatalf("round trip mismatch: got %d values, want %d", len(got), len(vals))
	}
	return r
}

func TestRoundTripRandomMixedSigns(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for _, n := range []int{1, 7, 128, 1000, 4097} {
		vals := make([]int64, n)
		for i := range vals {
			v := int64(rng.Uint64() % 2000)
			if rng.Intn(2) == 0 {
				v = -v
			}
			vals[i] = v
		}
		roundTrip(t, vals, 128)
	}
}

func TestSignedBoundaries(t *testing.T) {
	vals := []int64{
		math.MinInt64, math.MaxInt64, 0, -1, 1,
		math.MinInt64 + 1, math.MaxInt64 - 1,
		math.MinInt64, math.MaxInt64, 0,
	}
	r := roundTrip(t, vals, 4)
	for i, want := range vals {
		got, err := r.At(i)
		if err != nil || got != want {
			t.Fatalf("At(%d) = %d, %v; want %d", i, got, err, want)
		}
	}
}

func TestZigzagNoShiftOverflow(t *testing.T) {
	for _, v := range []int64{math.MinInt64, math.MaxInt64, -1, 0, 1} {
		if got := zigzagDecode(zigzagEncode(v)); got != v {
			t.Fatalf("zigzag round trip failed for %d", v)
		}
	}
}

func TestZeroValueBlockWidthZero(t *testing.T) {
	vals := make([]int64, 300) // all zeros
	r := roundTrip(t, vals, 128)
	layout, err := r.Layout()
	if err != nil {
		t.Fatal(err)
	}
	for _, bi := range layout {
		if bi.Width != 0 {
			t.Fatalf("zero block: width = %d, want 0", bi.Width)
		}
		if bi.Exceptions != 0 {
			t.Fatalf("zero block: exceptions = %d, want 0", bi.Exceptions)
		}
	}
}

func TestSingleLargeExceptionNotTruncated(t *testing.T) {
	vals := make([]int64, 128)
	for i := range vals {
		vals[i] = int64(i % 3) // tiny deltas
	}
	vals[77] = math.MaxInt64 // one huge outlier
	r := roundTrip(t, vals, 128)
	layout, err := r.Layout()
	if err != nil {
		t.Fatal(err)
	}
	if layout[0].Width > 2 {
		t.Fatalf("width = %d, one exception should keep it small", layout[0].Width)
	}
	if layout[0].Exceptions != 1 {
		t.Fatalf("exceptions = %d, want 1", layout[0].Exceptions)
	}
	got, err := r.At(77)
	if err != nil || got != math.MaxInt64 {
		t.Fatalf("At(77) = %d, %v; want MaxInt64 (truncated!)", got, err)
	}
}

func TestTailBlockLengthExplicit(t *testing.T) {
	vals := make([]int64, 130) // 128 + tail of 2
	for i := range vals {
		vals[i] = int64(i)
	}
	r := roundTrip(t, vals, 128)
	layout, err := r.Layout()
	if err != nil {
		t.Fatal(err)
	}
	if len(layout) != 2 || layout[1].Count != 2 {
		t.Fatalf("tail block count = %d, want explicit 2", layout[1].Count)
	}
}

func TestRangeDecode(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	vals := make([]int64, 1000)
	for i := range vals {
		vals[i] = int64(rng.Intn(500) - 250)
	}
	r := roundTrip(t, vals, 64)
	for _, span := range [][2]int{{0, 1000}, {63, 129}, {1, 2}, {999, 1000}, {200, 700}} {
		got, failed := r.DecodeRange(span[0], span[1])
		if len(failed) != 0 {
			t.Fatalf("range %v: unexpected failed blocks %v", span, failed)
		}
		if !reflect.DeepEqual(got, vals[span[0]:span[1]]) {
			t.Fatalf("range %v mismatch", span)
		}
	}
}

func TestCorruptionIsolation(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	vals := make([]int64, 512)
	for i := range vals {
		vals[i] = int64(rng.Intn(100))
	}
	data := Encode(vals, 128)
	r, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := r.Layout()
	if err != nil {
		t.Fatal(err)
	}
	// Single-point tamper: flip one byte inside block 1's payload.
	victim := 1
	data[layout[victim].Offset+4+3] ^= 0xFF

	r2, err := Open(data) // header/index untouched: opens fine
	if err != nil {
		t.Fatal(err)
	}
	got, failed := r2.DecodeRange(0, len(vals))
	if !reflect.DeepEqual(failed, []int{victim}) {
		t.Fatalf("failed blocks = %v, want [%d]", failed, victim)
	}
	// All other blocks decode and match the original list.
	want := append(append([]int64{}, vals[:victim*128]...), vals[(victim+1)*128:]...)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("isolation decode mismatch: got %d values, want %d", len(got), len(want))
	}
	if _, err := r2.DecodeAll(); err == nil {
		t.Fatal("DecodeAll should report the corrupted block")
	}
}

func TestPseudoLengthNotTrusted(t *testing.T) {
	vals := []int64{1, 2, 3, 4}
	data := Encode(vals, 4)
	r, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := r.Layout()
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt the count field to a huge pseudo length; CRC is recomputed
	// so the block parser itself must reject it before allocating.
	payloadOff := layout[0].Offset + 4
	data[payloadOff+2] = 0xFF // count low byte
	data[payloadOff+3] = 0xFF
	data[payloadOff+4] = 0xFF
	data[payloadOff+5] = 0x7F // count = 0x7FFFFFFF
	payload := data[payloadOff : payloadOff+layout[0].PayloadSize]
	crcOff := payloadOff + layout[0].PayloadSize
	putCRC32(data[crcOff:], payload)
	if _, err := r.Block(0); err == nil {
		t.Fatal("block with pseudo length must fail, not allocate")
	}
	// Direct decode with absurd count and tiny payload.
	tiny := []byte{0, 8, 0xFF, 0xFF, 0xFF, 0x7F, 0, 0}
	if _, err := decodeBlock(tiny, maxBlockCount); err == nil {
		t.Fatal("decodeBlock trusted a pseudo length")
	}
}

func TestSizeAccountingExact(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	vals := make([]int64, 1000)
	for i := range vals {
		vals[i] = int64(rng.Intn(10000) - 5000)
	}
	vals[500] = math.MaxInt64
	data := Encode(vals, 128)
	r, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := r.Layout()
	if err != nil {
		t.Fatal(err)
	}
	total := HeaderSize
	for _, bi := range layout {
		total += bi.RecordSize
	}
	total += len(layout) * 12 // index entries
	if total != r.Size() {
		t.Fatalf("accounted %d bytes, actual %d", total, r.Size())
	}
	// Cost model must match real payload sizes exactly.
	for _, bi := range layout {
		payload, err := r.blockPayload(bi.Index)
		if err != nil {
			t.Fatal(err)
		}
		if len(payload) != bi.PayloadSize {
			t.Fatalf("block %d payload size mismatch", bi.Index)
		}
	}
	t.Logf("encoded %d values into %d bytes (raw int64: %d bytes)",
		len(vals), r.Size(), len(vals)*8)
}

func TestOptimizerPicksMinimalWidth(t *testing.T) {
	// 127 small values + 1 outlier: exception must beat width inflation.
	small := make([]int64, 128)
	for i := range small {
		small[i] = int64(i % 4)
	}
	withOutlier := append(append([]int64{}, small[:64]...), append([]int64{1 << 40}, small[64:]...)...)
	r := roundTrip(t, withOutlier, 128)
	layout, _ := r.Layout()
	if layout[0].Width > 2 || layout[0].Exceptions != 1 {
		t.Fatalf("optimizer failed: width=%d exc=%d", layout[0].Width, layout[0].Exceptions)
	}
	// Uniformly wide data: no exceptions, full width.
	wide := make([]int64, 64)
	rng := rand.New(rand.NewSource(1))
	for i := range wide {
		wide[i] = int64(rng.Uint64())
	}
	r2 := roundTrip(t, wide, 64)
	l2, _ := r2.Layout()
	if l2[0].Width != 64 || l2[0].Exceptions != 0 {
		t.Fatalf("wide data: width=%d exc=%d", l2[0].Width, l2[0].Exceptions)
	}
}

func TestEmptyAndSingleValue(t *testing.T) {
	r := roundTrip(t, []int64{}, 16)
	if r.Len() != 0 || r.NumBlocks() != 0 {
		t.Fatal("empty archive should have no blocks")
	}
	roundTrip(t, []int64{-42}, 16)
}

func TestRandomAccessDoesNotDecodeAll(t *testing.T) {
	vals := make([]int64, 256)
	for i := range vals {
		vals[i] = int64(i * 7)
	}
	data := Encode(vals, 64)
	r, _ := Open(data)
	// Corrupt block 0; random access into block 3 must still succeed,
	// proving At only touches the target block.
	layout, _ := r.Layout()
	data[layout[0].Offset+5] ^= 0x01
	got, err := r.At(200)
	if err != nil || got != 1400 {
		t.Fatalf("At(200) = %d, %v; want 1400", got, err)
	}
	if _, err := r.At(10); err == nil {
		t.Fatal("At into corrupted block should fail")
	}
}
