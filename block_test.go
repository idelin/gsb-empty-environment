package secded

import (
	"bytes"
	"math/rand"
	"testing"
)

func newTestBlockCodec(t *testing.T) *BlockCodec {
	t.Helper()
	c, err := New(6) // Hamming(64,57) extended
	if err != nil {
		t.Fatal(err)
	}
	bc, err := NewBlockCodec(c, DefaultDepth)
	if err != nil {
		t.Fatal(err)
	}
	return bc
}

func encodeBlocks(t *testing.T, bc *BlockCodec, data []byte) []byte {
	t.Helper()
	nBlocks := (len(data)*8 + bc.BlockDataBits() - 1) / bc.BlockDataBits()
	stream := make([]byte, nBlocks*bc.BlockStreamBytes())
	padded := make([]byte, nBlocks*bc.BlockDataBits()/8)
	copy(padded, data)
	for blk := 0; blk < nBlocks; blk++ {
		bc.EncodeBlock(padded[blk*bc.BlockDataBits()/8:], stream, blk*bc.BlockStreamBits())
	}
	return stream
}

func flipStream(stream []byte, start, n int) []byte {
	out := append([]byte(nil), stream...)
	for t := start; t < start+n && t/8 < len(out); t++ {
		out[t/8] ^= 1 << uint(t%8)
	}
	return out
}

func decodeAll(bc *BlockCodec, stream []byte, nDataBytes int, erasures []int) ([]byte, []BlockReport) {
	nBlocks := len(stream) / bc.BlockStreamBytes()
	out := make([]byte, nBlocks*bc.BlockDataBits()/8)
	reps := make([]BlockReport, nBlocks)
	for blk := 0; blk < nBlocks; blk++ {
		var per []int
		lo, hi := blk*bc.BlockStreamBits(), (blk+1)*bc.BlockStreamBits()
		for _, e := range erasures {
			if e >= lo && e < hi {
				per = append(per, e-lo)
			}
		}
		reps[blk] = bc.DecodeBlock(stream, blk*bc.BlockStreamBits(), per, out, blk*bc.BlockDataBits())
	}
	return out[:nDataBytes], reps
}

func TestBlockRoundTrip(t *testing.T) {
	bc := newTestBlockCodec(t)
	rng := rand.New(rand.NewSource(1))
	data := make([]byte, 1000)
	rng.Read(data)
	stream := encodeBlocks(t, bc, data)
	got, reps := decodeAll(bc, stream, len(data), nil)
	if !bytes.Equal(got, data) {
		t.Fatal("round trip mismatch")
	}
	for _, r := range reps {
		if !r.OK {
			t.Fatalf("block %d not OK", r.Block)
		}
	}
}

// A burst of <= Depth bits hits each word at most once: correctable.
func TestBurstWithinDepth(t *testing.T) {
	bc := newTestBlockCodec(t)
	rng := rand.New(rand.NewSource(2))
	data := make([]byte, 500)
	rng.Read(data)
	stream := encodeBlocks(t, bc, data)
	corrupt := flipStream(stream, 13, bc.Depth)
	got, reps := decodeAll(bc, corrupt, len(data), nil)
	if !bytes.Equal(got, data) {
		t.Fatal("burst of Depth bits not fully corrected")
	}
	corrected := 0
	for _, r := range reps {
		for _, w := range r.Words {
			if w.Fate == FateCorrected {
				corrected++
			}
		}
	}
	if corrected == 0 {
		t.Fatal("expected corrections to be reported")
	}
}

// A burst of 2*Depth bits, unmarked: some words see 2 errors ->
// detected as uncorrectable, never silently "repaired".
func TestBurstDoubleDepthUnmarked(t *testing.T) {
	bc := newTestBlockCodec(t)
	data := bytes.Repeat([]byte("xy"), 250)
	stream := encodeBlocks(t, bc, data)
	corrupt := flipStream(stream, 0, 2*bc.Depth)
	_, reps := decodeAll(bc, corrupt, len(data), nil)
	if reps[0].OK {
		t.Fatal("2*Depth burst should be uncorrectable when unmarked")
	}
	unc := 0
	for _, w := range reps[0].Words {
		if w.Fate == FateUncorrectable {
			unc++
		}
		if w.Fate == FateCorrected || w.Fate == FateErasureRecovered {
			t.Fatalf("word %d falsely claimed repaired", w.Word)
		}
	}
	if unc == 0 {
		t.Fatal("expected uncorrectable words")
	}
}

// A burst of 3*Depth bits, marked as erasures: <=3 erasures per word,
// recoverable.
func TestBurstTripleDepthMarked(t *testing.T) {
	bc := newTestBlockCodec(t)
	rng := rand.New(rand.NewSource(3))
	data := make([]byte, 500)
	rng.Read(data)
	stream := encodeBlocks(t, bc, data)
	start := 8 * bc.Depth // block-aligned for a clean 3-per-word split
	corrupt := flipStream(stream, start, 3*bc.Depth)
	erasures := make([]int, 3*bc.Depth)
	for i := range erasures {
		erasures[i] = start + i
	}
	got, reps := decodeAll(bc, corrupt, len(data), erasures)
	if !bytes.Equal(got, data) {
		t.Fatal("marked 3*Depth burst not recovered")
	}
	rec := 0
	for _, r := range reps {
		for _, w := range r.Words {
			if w.Fate == FateErasureRecovered {
				rec++
			}
		}
	}
	if rec == 0 {
		t.Fatal("expected erasure recoveries to be reported")
	}
}

// A burst of 3*Depth+1 bits, marked: some word gets 4 erasures ->
// beyond capability, reported uncorrectable.
func TestBurstBeyondCapabilityMarked(t *testing.T) {
	bc := newTestBlockCodec(t)
	data := bytes.Repeat([]byte("ab"), 250)
	stream := encodeBlocks(t, bc, data)
	start := 0
	corrupt := flipStream(stream, start, 3*bc.Depth+1)
	erasures := make([]int, 3*bc.Depth+1)
	for i := range erasures {
		erasures[i] = start + i
	}
	_, reps := decodeAll(bc, corrupt, len(data), erasures)
	if reps[0].OK {
		t.Fatal("burst beyond capability must not decode clean")
	}
}

// Random access: decoding a single block at its offset matches the
// corresponding slice of a full sequential decode.
func TestRandomAccessBlock(t *testing.T) {
	bc := newTestBlockCodec(t)
	rng := rand.New(rand.NewSource(4))
	data := make([]byte, 2000)
	rng.Read(data)
	stream := encodeBlocks(t, bc, data)
	full, _ := decodeAll(bc, stream, len(data), nil)

	const blk = 2
	oneBlock := make([]byte, bc.BlockDataBits()/8)
	rep := bc.DecodeBlock(stream, blk*bc.BlockStreamBits(), nil, oneBlock, 0)
	if !rep.OK {
		t.Fatal("block decode failed")
	}
	off := blk * bc.BlockDataBits() / 8
	if !bytes.Equal(oneBlock, full[off:off+len(oneBlock)]) {
		t.Fatal("random-access block does not match sequential decode")
	}
}
