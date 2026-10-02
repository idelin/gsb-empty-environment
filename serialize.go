package bptree

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

var (
	ErrBadMagic   = errors.New("bptree: bad magic in serialized data")
	ErrTruncated  = errors.New("bptree: truncated serialized data")
	ErrCorruptSum = errors.New("bptree: stored summaries do not match recomputed min-excess/count values")
)

var magic = []byte("BPT1")

// Serialized layout (all integers little-endian):
//
//	magic[4] "BPT1"
//	u64 nbits, u64 nwords, words[nwords]u64
//	u64 nsupers, supers[nsupers]{i64 excess, i64 min, i64 opens, i64 total}
//	u64 nblocks, blocks[nblocks]{i32 base, i16 total, i16 relMin, u16 relOpen}
//
// On load the summaries (including the minimum prefix excess and the
// paren counts) are recomputed from the bitvector and every stored field
// is verified; any mismatch is rejected.
func (ix *Index) MarshalBinary() []byte {
	var buf bytes.Buffer
	buf.Write(magic)
	putU64 := func(v uint64) { var b [8]byte; binary.LittleEndian.PutUint64(b[:], v); buf.Write(b[:]) }
	putU64(uint64(ix.n))
	putU64(uint64(len(ix.words)))
	for _, w := range ix.words {
		putU64(w)
	}
	putU64(uint64(len(ix.supers)))
	for _, s := range ix.supers {
		putU64(uint64(s.excess))
		putU64(uint64(s.min))
		putU64(uint64(s.opens))
		putU64(uint64(s.total))
	}
	putU64(uint64(len(ix.blocks)))
	for _, b := range ix.blocks {
		var b10 [10]byte
		binary.LittleEndian.PutUint32(b10[0:4], uint32(b.base))
		binary.LittleEndian.PutUint16(b10[4:6], uint16(b.total))
		binary.LittleEndian.PutUint16(b10[6:8], uint16(b.relMin))
		binary.LittleEndian.PutUint16(b10[8:10], b.relOpen)
		buf.Write(b10[:])
	}
	return buf.Bytes()
}

// Unmarshal parses a serialized index and verifies it: the parentheses
// sequence is re-validated (non-negative prefix balance, zero total) and
// every stored superblock/block summary is compared against values
// recomputed from the bitvector.
func Unmarshal(data []byte) (*Index, error) {
	if len(data) < 4 || !bytes.Equal(data[:4], magic) {
		return nil, ErrBadMagic
	}
	r := data[4:]
	getU64 := func() (uint64, error) {
		if len(r) < 8 {
			return 0, ErrTruncated
		}
		v := binary.LittleEndian.Uint64(r[:8])
		r = r[8:]
		return v, nil
	}
	nbits, err := getU64()
	if err != nil {
		return nil, err
	}
	nw, err := getU64()
	if err != nil {
		return nil, err
	}
	if nw != (nbits+BlockSize-1)/BlockSize {
		return nil, ErrCorruptSum
	}
	words := make([]uint64, nw)
	for i := range words {
		if words[i], err = getU64(); err != nil {
			return nil, err
		}
	}
	nsup, err := getU64()
	if err != nil {
		return nil, err
	}
	supers := make([]superSummary, nsup)
	for i := range supers {
		e, err1 := getU64()
		m, err2 := getU64()
		o, err3 := getU64()
		t, err4 := getU64()
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			return nil, ErrTruncated
		}
		supers[i] = superSummary{excess: int64(e), min: int64(m), opens: int64(o), total: int64(t)}
	}
	nblk, err := getU64()
	if err != nil {
		return nil, err
	}
	blocks := make([]blockSummary, nblk)
	for i := range blocks {
		if len(r) < 10 {
			return nil, ErrTruncated
		}
		blocks[i] = blockSummary{
			base:    int32(binary.LittleEndian.Uint32(r[0:4])),
			total:   int16(binary.LittleEndian.Uint16(r[4:6])),
			relMin:  int16(binary.LittleEndian.Uint16(r[6:8])),
			relOpen: binary.LittleEndian.Uint16(r[8:10]),
		}
		r = r[10:]
	}
	// Rebuild from the bitvector alone: re-validates balance and recomputes
	// every summary, then require exact equality with the stored tables.
	fresh, err := Build(words, int(nbits))
	if err != nil {
		return nil, err
	}
	if len(supers) != len(fresh.supers) || len(blocks) != len(fresh.blocks) {
		return nil, ErrCorruptSum
	}
	for i := range supers {
		if supers[i] != fresh.supers[i] {
			return nil, fmt.Errorf("%w: superblock %d", ErrCorruptSum, i)
		}
	}
	for i := range blocks {
		if blocks[i] != fresh.blocks[i] {
			return nil, fmt.Errorf("%w: block %d", ErrCorruptSum, i)
		}
	}
	// Count check on the verified tables: total opens must equal nbits/2.
	last := len(fresh.blocks) - 1
	totalOpens := fresh.supers[len(fresh.supers)-1].opens + int64(fresh.blocks[last].relOpen)
	nb := fresh.n % BlockSize
	if nb == 0 {
		nb = BlockSize
	}
	w := fresh.words[last]
	for i := 0; i < nb; i++ {
		totalOpens += int64(w >> uint(i) & 1)
	}
	if totalOpens != int64(fresh.nodes) {
		return nil, fmt.Errorf("%w: open-paren count %d != %d", ErrCorruptSum, totalOpens, fresh.nodes)
	}
	return fresh, nil
}

// Layout describes the real memory layout and byte occupancy of the index.
type Layout struct {
	Nodes          int
	Bits           int
	Words          int
	NumBlocks      int
	NumSupers      int
	BitvectorBytes int
	BlockBytes     int
	SuperBytes     int
	TotalBytes     int
	BitsPerNode    float64
	OverheadPct    float64 // summary tables relative to the bitvector
}

// Layout returns the byte occupancy of the bitvector and summary tables.
func (ix *Index) Layout() Layout {
	bv := len(ix.words) * 8
	blk := len(ix.blocks) * 10
	sup := len(ix.supers) * 32
	tot := bv + blk + sup
	return Layout{
		Nodes:          ix.nodes,
		Bits:           ix.n,
		Words:          len(ix.words),
		NumBlocks:      len(ix.blocks),
		NumSupers:      len(ix.supers),
		BitvectorBytes: bv,
		BlockBytes:     blk,
		SuperBytes:     sup,
		TotalBytes:     tot,
		BitsPerNode:    float64(tot) * 8 / float64(ix.nodes),
		OverheadPct:    float64(blk+sup) * 100 / float64(bv),
	}
}
