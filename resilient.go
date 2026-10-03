package huffsync

import "fmt"

// SyncWord is a 32-bit self-synchronising marker inserted between segments.
// The encoder verifies it never appears inside a segment payload, so any
// occurrence in the stream is a genuine frame boundary.
const SyncWord uint32 = 0x1ACFFC1D

// SyncLen is the sync word length in bits.
const SyncLen = 32

// SegmentBytes is the fixed payload size of one segment. Segments are
// zero-padded to exactly this size so sync words land on predictable byte
// offsets (0, 100, 200, ... with the defaults).
const SegmentBytes = 96

// FrameBytes is the wire size of one frame: sync word plus segment.
const FrameBytes = SegmentBytes + SyncLen/8

// EncodedStream is a framed, sync-protected Huffman bit stream.
type EncodedStream struct {
	Bits *BitString
	// SymbolsPerSegment records how many source symbols each segment
	// carries; used by tests and tooling to measure salvage ratios.
	SymbolsPerSegment []int
}

// EncodeResilient frames msg as [SYNC][segment][SYNC][segment]... .
//
// Each segment packs whole codewords into SegmentBytes*8 bits, always
// reserving room for the SymInvalid terminator; the terminator plus zero
// padding fills the tail. Because SymInvalid never appears in data, a
// decoder that reaches it knows the segment ended cleanly; conversely a
// segment that ends by bit exhaustion was damaged.
func EncodeResilient(msg []byte, c *HuffCode) *EncodedStream {
	budget := SegmentBytes * 8
	invLen := c.CodeLen(SymInvalid)
	b := &BitString{}
	var segCounts []int
	segBits, segSyms := 0, 0

	startSegment := func() {
		b.appendBits(uint64(SyncWord), SyncLen)
		segBits, segSyms = 0, 0
	}
	closeSegment := func() {
		b.appendBits(c.code[SymInvalid], invLen)
		segBits += invLen
		for segBits < budget {
			b.appendBit(false)
			segBits++
		}
		segCounts = append(segCounts, segSyms)
	}

	startSegment()
	for _, s := range msg {
		l := c.CodeLen(int(s))
		if segBits+l+invLen > budget {
			closeSegment()
			startSegment()
		}
		c.encodeTo(b, int(s))
		segBits += l
		segSyms++
	}
	closeSegment()

	// Guarantee the self-synchronising property: the sync word must occur
	// exactly at frame boundaries and nowhere else.
	syncs := FindSync(b)
	if len(syncs) != len(segCounts) {
		panic(fmt.Sprintf("huffsync: sync word collision in payload (%d extra occurrences)", len(syncs)-len(segCounts)))
	}
	for i, p := range syncs {
		if p != i*FrameBytes*8 {
			panic(fmt.Sprintf("huffsync: sync word at unexpected bit %d (frame %d)", p, i))
		}
	}
	return &EncodedStream{Bits: b, SymbolsPerSegment: segCounts}
}

// FindSync returns every bit offset at which the sync word occurs.
func FindSync(b *BitString) []int {
	var pos []int
	for i := 0; i+SyncLen <= b.Len(); i++ {
		match := true
		for j := 0; j < SyncLen; j++ {
			want := SyncWord>>uint(SyncLen-1-j)&1 == 1
			if b.At(i+j) != want {
				match = false
				break
			}
		}
		if match {
			pos = append(pos, i)
		}
	}
	return pos
}

// SegmentResult describes one decoded segment between two sync words.
type SegmentResult struct {
	Index      int    // segment number (0 follows the first sync word)
	SyncBitPos int    // bit offset of the preceding sync word in this stream
	Symbols    []byte // salvaged symbols (prefix may be valid, tail may be garbage)
	CleanEnd   bool   // true if the segment ended at its SymInvalid terminator
}

// DecodeResult is the outcome of a resilient decode pass.
type DecodeResult struct {
	Segments []SegmentResult
	Output   []byte // concatenation of all salvaged segment symbols
}

// DecodeResilient re-locks phase at every sync word and decodes each
// segment independently, confining any corruption to the segment that
// contains it. It tolerates arbitrary bit insertions, deletions and flips
// between sync words: after the damage, decoding resumes cleanly at the
// next sync word.
func DecodeResilient(b *BitString, c *HuffCode) *DecodeResult {
	syncs := FindSync(b)
	res := &DecodeResult{}
	for i, sp := range syncs {
		start := sp + SyncLen
		end := b.Len()
		if i+1 < len(syncs) {
			end = syncs[i+1]
		}
		seg := SegmentResult{Index: i, SyncBitPos: sp}
		pos := start
		for pos < end {
			sym, next, ok := c.decode(b, pos, end)
			if !ok {
				break // bit exhaustion mid-codeword: corrupted tail
			}
			pos = next
			if sym == SymInvalid {
				seg.CleanEnd = true
				break
			}
			seg.Symbols = append(seg.Symbols, byte(sym))
		}
		res.Segments = append(res.Segments, seg)
		res.Output = append(res.Output, seg.Symbols...)
	}
	return res
}
