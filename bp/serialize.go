package bp

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const magic uint32 = 0x42505431 // "BPT1"

// Serialized layout (little-endian):
//
//	u32 magic
//	u64 n                       number of parentheses
//	i32 minPrefixExcess         global minimum prefix excess (0 for a tree)
//	i32 totalExcess             final excess (0 for a tree)
//	u64 numWords, then words    packed parentheses, 1 bit each
//	u64 numSB,   then entries   (i32 absExcess, i32 absOpens, i16 relMin, i16 pad)
//	u64 numBlk,  then entries   (i16 relExcess, i16 relMin, i16 relOpens)
//
// Load re-derives everything from the bits and rejects the blob if the
// stored min prefix excess, excess totals, open counts or any summary entry
// disagree with the recomputed values.
func (ix *Index) MarshalBinary() []byte {
	minE, total := ix.prefixStats()
	buf := make([]byte, 0, ix.serializedSize())
	putU16 := func(v int16) { buf = binary.LittleEndian.AppendUint16(buf, uint16(v)) }
	putU32 := func(v uint32) { buf = binary.LittleEndian.AppendUint32(buf, v) }
	putU64 := func(v uint64) { buf = binary.LittleEndian.AppendUint64(buf, v) }
	putU32(magic)
	putU64(uint64(ix.n))
	putU32(uint32(minE))
	putU32(uint32(total))
	putU64(uint64(len(ix.bits.words)))
	for _, w := range ix.bits.words {
		putU64(w)
	}
	putU64(uint64(len(ix.sbs)))
	for _, sb := range ix.sbs {
		putU32(uint32(sb.absExcess))
		putU32(uint32(sb.absOpens))
		putU16(sb.relMin)
		putU16(0)
	}
	putU64(uint64(len(ix.blks)))
	for _, blk := range ix.blks {
		putU16(blk.relExcess)
		putU16(blk.relMin)
		putU16(blk.relOpens)
	}
	return buf
}

func (ix *Index) serializedSize() int {
	return 4 + 8 + 4 + 4 + 8 + 8*len(ix.bits.words) + 8 + 12*len(ix.sbs) + 8 + 6*len(ix.blks)
}

// prefixStats walks the bits once and returns the global minimum prefix
// excess and the final excess.
func (ix *Index) prefixStats() (minExcess, total int) {
	excess := 0
	for i := 0; i < ix.n; i++ {
		if ix.bits.get(i) {
			excess++
		} else {
			excess--
		}
		if excess < minExcess {
			minExcess = excess
		}
	}
	return minExcess, excess
}

// Load parses a serialized index and verifies it against the raw bits:
// balance, non-negative prefixes, single root, stored min/total excess,
// open counts, and every superblock/block summary entry.
func Load(data []byte) (*Index, error) {
	r := &reader{data: data}
	if r.u32() != magic {
		return nil, errors.New("bp: bad magic")
	}
	n := int(r.u64())
	if n < 2 || n%2 != 0 {
		return nil, fmt.Errorf("bp: invalid parenthesis count %d", n)
	}
	storedMin := int32(r.u32())
	storedTotal := int32(r.u32())
	numWords := int(r.u64())
	if numWords != (n+63)/64 {
		return nil, fmt.Errorf("bp: word count %d does not match n=%d", numWords, n)
	}
	words := make([]uint64, numWords)
	for i := range words {
		words[i] = r.u64()
	}
	numSB := int(r.u64())
	if numSB != (n+SuperblockSize-1)/SuperblockSize {
		return nil, fmt.Errorf("bp: superblock count %d does not match n=%d", numSB, n)
	}
	sbs := make([]superblock, numSB)
	for i := range sbs {
		sbs[i].absExcess = int32(r.u32())
		sbs[i].absOpens = int32(r.u32())
		sbs[i].relMin = int16(r.u16())
		if r.u16() != 0 {
			return nil, fmt.Errorf("bp: nonzero superblock padding at %d", i)
		}
	}
	numBlk := int(r.u64())
	if numBlk != (n+BlockSize-1)/BlockSize {
		return nil, fmt.Errorf("bp: block count %d does not match n=%d", numBlk, n)
	}
	blks := make([]block, numBlk)
	for i := range blks {
		blks[i].relExcess = int16(r.u16())
		blks[i].relMin = int16(r.u16())
		blks[i].relOpens = int16(r.u16())
	}
	if r.err != nil {
		return nil, r.err
	}
	if r.off != len(data) {
		return nil, fmt.Errorf("bp: %d trailing bytes", len(data)-r.off)
	}
	if rem := uint(n) & 63; rem != 0 && words[numWords-1]>>rem != 0 {
		return nil, errors.New("bp: nonzero padding bits beyond n")
	}

	ix := &Index{n: n, nodes: n / 2}
	ix.bits = bitvec{words: words, n: n}
	ix.sbs = sbs
	ix.blks = blks

	// Re-verify the parenthesis string itself.
	excess, opens, minE := 0, 0, 0
	for i := 0; i < n; i++ {
		if ix.bits.get(i) {
			excess++
			opens++
		} else {
			excess--
		}
		if excess < 0 {
			return nil, fmt.Errorf("bp: negative prefix balance at position %d", i)
		}
		if excess == 0 && i < n-1 {
			return nil, fmt.Errorf("bp: balance returns to 0 at position %d: not a single tree", i)
		}
		if excess < minE {
			minE = excess
		}
	}
	if excess != 0 {
		return nil, fmt.Errorf("bp: final excess %d != 0", excess)
	}
	if opens != n/2 {
		return nil, fmt.Errorf("bp: open count %d != n/2", opens)
	}
	if int32(minE) != storedMin {
		return nil, fmt.Errorf("bp: stored min prefix excess %d != recomputed %d", storedMin, minE)
	}
	if int32(excess) != storedTotal {
		return nil, fmt.Errorf("bp: stored total excess %d != recomputed %d", storedTotal, excess)
	}

	// Re-verify every summary entry against the bits.
	wantSB, wantBlk := ix.computeSummaries()
	for i := range sbs {
		if sbs[i] != wantSB[i] {
			return nil, fmt.Errorf("bp: superblock %d summary mismatch", i)
		}
	}
	for i := range blks {
		if blks[i] != wantBlk[i] {
			return nil, fmt.Errorf("bp: block %d summary mismatch", i)
		}
	}
	ix.buildRMQ()
	return ix, nil
}

var errTruncated = errors.New("bp: truncated data")

type reader struct {
	data []byte
	off  int
	err  error
}

func (r *reader) take(k int) []byte {
	if r.err != nil {
		return nil
	}
	if r.off+k > len(r.data) {
		r.err = errTruncated
		return nil
	}
	b := r.data[r.off : r.off+k]
	r.off += k
	return b
}

func (r *reader) u16() uint16 {
	b := r.take(2)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint16(b)
}

func (r *reader) u32() uint32 {
	b := r.take(4)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}

func (r *reader) u64() uint64 {
	b := r.take(8)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint64(b)
}
