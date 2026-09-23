package secded

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"
)

func encodeBytes(t *testing.T, payload []byte, k, depth int) ([]byte, EncodeReport) {
	t.Helper()
	enc, err := NewEncoder(k, depth)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	rep, err := enc.Encode(&buf, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), rep
}

func flipFileBit(data []byte, bit uint64) {
	data[bit/8] ^= 1 << (7 - bit%8)
}

func TestStreamRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, k := range []int{4, 8, 11, 26} {
		for _, depth := range []int{1, 4, 8} {
			for _, size := range []int{0, 1, 3, 100, 1000} {
				payload := make([]byte, size)
				rng.Read(payload)
				stream, _ := encodeBytes(t, payload, k, depth)
				got, _, rep, err := Decode(bytes.NewReader(stream), nil, 1<<20)
				if err != nil {
					t.Fatalf("k=%d d=%d n=%d: %v", k, depth, size, err)
				}
				if !bytes.Equal(got, payload) {
					t.Fatalf("k=%d d=%d n=%d: payload mismatch", k, depth, size)
				}
				if !rep.PayloadCRCOK || len(rep.Uncorrectable) != 0 {
					t.Fatalf("k=%d d=%d n=%d: bad report", k, depth, size)
				}
			}
		}
	}
}

// TestBurstWithinDepth: a burst of <= depth consecutive body bits hits at
// most one bit per block and must be fully corrected.
func TestBurstWithinDepth(t *testing.T) {
	payload := bytes.Repeat([]byte("burst-"), 40)
	const depth = 8
	stream, _ := encodeBytes(t, payload, 11, depth)
	bodyStart := uint64(HeaderSize * 8)
	for i := uint64(0); i < depth; i++ {
		flipFileBit(stream, bodyStart+i)
	}
	got, _, decRep, err := Decode(bytes.NewReader(stream), nil, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("burst within depth not fully corrected")
	}
	corrected := 0
	for _, b := range decRep.Blocks {
		if b.Status == StatusCorrectedSingle.String() {
			corrected++
		}
	}
	if corrected != depth {
		t.Fatalf("expected %d corrected blocks, got %d", depth, corrected)
	}
}

// TestBurstBeyondDepth: a burst of 2*depth bits gives some blocks 2 errors;
// those must be flagged uncorrectable (never silently "fixed") and the
// payload CRC must fail.
func TestBurstBeyondDepth(t *testing.T) {
	payload := bytes.Repeat([]byte("burst-"), 40)
	const depth = 8
	stream, _ := encodeBytes(t, payload, 11, depth)
	bodyStart := uint64(HeaderSize * 8)
	// Start the burst at codeword position 3 (a data bit) so each block
	// takes one data-bit hit plus one parity-bit hit: two errors per block.
	for i := uint64(3 * depth); i < 5*depth; i++ {
		flipFileBit(stream, bodyStart+i)
	}
	_, _, rep, err := Decode(bytes.NewReader(stream), nil, 1<<20)
	if !errors.Is(err, ErrDataCorrupt) {
		t.Fatalf("want ErrDataCorrupt, got %v", err)
	}
	if rep.PayloadCRCOK {
		t.Fatal("CRC must fail after uncorrectable burst")
	}
	if len(rep.Uncorrectable) == 0 {
		t.Fatal("expected uncorrectable blocks in report")
	}
	for _, blk := range rep.Uncorrectable {
		if rep.Blocks[blk].Status != StatusDoubleError.String() {
			t.Fatalf("block %d status %q, want double-error-detected", blk, rep.Blocks[blk].Status)
		}
	}
}

// TestErasureMarkers: up to 3 erased bits per block are recovered.
func TestErasureMarkers(t *testing.T) {
	payload := []byte("erasure recovery across the interleaved frame layout")
	stream, rep := encodeBytes(t, payload, 11, 4)
	erasures := &BitSet{}
	// Erase 3 bits in block 0 and 1 bit in block 1 (body-relative offsets).
	p, _ := rep.Header.Params()
	for _, pos := range []int{0, 5, 9} {
		off, err := rep.Header.BitOffset(0, pos)
		if err != nil {
			t.Fatal(err)
		}
		erasures.Set(off)
	}
	off, err := rep.Header.BitOffset(1, 3)
	if err != nil {
		t.Fatal(err)
	}
	erasures.Set(off)
	got, _, decRep, err := Decode(bytes.NewReader(stream), erasures, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("erasure recovery failed")
	}
	if decRep.Blocks[0].Status != StatusRecoveredErasure.String() ||
		decRep.Blocks[1].Status != StatusRecoveredErasure.String() {
		t.Fatalf("unexpected statuses: %v %v", decRep.Blocks[0].Status, decRep.Blocks[1].Status)
	}
	_ = p
}

// TestRandomAccessBitOffset: the documented BitOffset mapping must agree
// with the actual stream layout — corrupting one block through its
// computed offsets must surface as a single corrected error in that block.
func TestRandomAccessBitOffset(t *testing.T) {
	payload := bytes.Repeat([]byte{0xA5}, 64)
	stream, rep := encodeBytes(t, payload, 11, 8)
	const target = 5
	off, err := rep.Header.BitOffset(target, 7)
	if err != nil {
		t.Fatal(err)
	}
	flipFileBit(stream, uint64(HeaderSize*8)+off)
	_, _, decRep, err := Decode(bytes.NewReader(stream), nil, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	br := decRep.Blocks[target]
	if br.Status != StatusCorrectedSingle.String() || len(br.Flipped) != 1 || br.Flipped[0] != 7 {
		t.Fatalf("block %d: %+v", target, br)
	}
	for i, b := range decRep.Blocks {
		if i != target && b.Status != StatusClean.String() {
			t.Fatalf("block %d unexpectedly %v", i, b.Status)
		}
	}
}

// TestSourceMapConsistency: every source-map entry must point at the body
// bit that actually carries that payload bit.
func TestSourceMapConsistency(t *testing.T) {
	payload := []byte("source map check")
	stream, rep := encodeBytes(t, payload, 8, 4)
	if int(rep.SourceMap.Bits[len(rep.SourceMap.Bits)-1].PayloadBit) != len(payload)*8-1 {
		t.Fatal("source map does not cover all payload bits")
	}
	// Flip the body bit of one mapped entry; decoding must show exactly one
	// corrected bit in the mapped block.
	entry := rep.SourceMap.Bits[10]
	flipFileBit(stream, uint64(HeaderSize*8)+entry.BodyBit)
	_, _, decRep, err := Decode(bytes.NewReader(stream), nil, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	br := decRep.Blocks[entry.Block]
	if br.Status != StatusCorrectedSingle.String() || br.Flipped[0] != entry.CodewordPos {
		t.Fatalf("map entry %+v inconsistent with report %+v", entry, br)
	}
}

// TestDecodeBound: payload lengths above the caller's bound are rejected
// before decoding.
func TestDecodeBound(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 100)
	stream, _ := encodeBytes(t, payload, 11, 4)
	_, _, _, err := Decode(bytes.NewReader(stream), nil, 50)
	if !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("want ErrOutputTooLarge, got %v", err)
	}
}

// TestHeaderValidation: bad magic, wrong version, corrupted header CRC and
// truncated bodies are all rejected.
func TestHeaderValidation(t *testing.T) {
	payload := []byte("header checks")
	stream, _ := encodeBytes(t, payload, 11, 4)

	bad := bytes.Clone(stream)
	bad[0] = 'X'
	if _, _, _, err := Decode(bytes.NewReader(bad), nil, 1<<20); err == nil {
		t.Fatal("bad magic accepted")
	}

	bad = bytes.Clone(stream)
	bad[4] = 99 // version; header CRC now stale too, which is also fine
	if _, _, _, err := Decode(bytes.NewReader(bad), nil, 1<<20); err == nil {
		t.Fatal("bad version accepted")
	}

	bad = bytes.Clone(stream)
	flipFileBit(bad, 100) // inside header, breaks header CRC
	if _, _, _, err := Decode(bytes.NewReader(bad), nil, 1<<20); err == nil {
		t.Fatal("corrupted header accepted")
	}

	if _, _, _, err := Decode(bytes.NewReader(stream[:len(stream)-1]), nil, 1<<20); err == nil {
		t.Fatal("truncated body accepted")
	}
}

// TestUncorrectedBlocksRecorded: the report preserves the state of blocks
// that could not be corrected.
func TestUncorrectedBlocksRecorded(t *testing.T) {
	payload := bytes.Repeat([]byte("z"), 32)
	stream, rep := encodeBytes(t, payload, 11, 4)
	// Two flips inside block 2 -> double error, must stay uncorrected.
	for _, pos := range []int{2, 6} {
		off, err := rep.Header.BitOffset(2, pos)
		if err != nil {
			t.Fatal(err)
		}
		flipFileBit(stream, uint64(HeaderSize*8)+off)
	}
	_, _, decRep, err := Decode(bytes.NewReader(stream), nil, 1<<20)
	if !errors.Is(err, ErrDataCorrupt) {
		t.Fatalf("want ErrDataCorrupt, got %v", err)
	}
	if len(decRep.Uncorrectable) != 1 || decRep.Uncorrectable[0] != 2 {
		t.Fatalf("uncorrectable list = %v, want [2]", decRep.Uncorrectable)
	}
	if decRep.Blocks[2].Status != StatusDoubleError.String() {
		t.Fatalf("block 2 status = %v", decRep.Blocks[2].Status)
	}
}
