package streamcol

import (
	"encoding/binary"
	"hash/crc32"
	"sort"
)

// IndexEntry locates one decoded block for random access. Baseline is the
// absolute anchor value, so entries are self-sufficient: decoding a block
// through its entry never needs the previous block, even for dependent
// blocks.
type IndexEntry struct {
	Offset      int64  // absolute stream offset of the block's first byte
	FirstIndex  uint64 // stream-wide index of the block's first element
	Count       uint64
	Baseline    int64 // absolute anchor (value logically preceding element 0)
	LastValue   int64
	Independent bool
	Enc         Encoding
}

// Index is an ordered random-access index over the blocks of a stream.
type Index []IndexEntry

// Find returns the entry covering element k. Gaps left by skipped corrupt
// blocks simply do not match.
func (idx Index) Find(k uint64) (IndexEntry, bool) {
	i := sort.Search(len(idx), func(i int) bool { return idx[i].FirstIndex > k }) - 1
	if i < 0 {
		return IndexEntry{}, false
	}
	e := idx[i]
	if k >= e.FirstIndex+e.Count {
		return IndexEntry{}, false
	}
	return e, true
}

// DecodeAt returns the k-th element of the stream using idx to locate its
// block in data. The result is identical to sequential decoding.
func DecodeAt(data []byte, idx Index, k uint64) (int64, error) {
	e, ok := idx.Find(k)
	if !ok {
		return 0, ErrIndexOutOfRange
	}
	if e.Offset < 0 || e.Offset >= int64(len(data)) {
		return 0, ErrIndexOutOfRange
	}
	rest := data[e.Offset:]
	h, err := parseHeader(rest)
	if err != nil {
		return 0, err
	}
	total := uint64(h.headerLen) + h.payloadLen + crcLen
	if total > uint64(len(rest)) {
		return 0, ErrCorruptPayload
	}
	block := rest[:total]
	want := binary.LittleEndian.Uint32(block[len(block)-crcLen:])
	if crc32.Checksum(block[2:len(block)-crcLen], castagnoli) != want {
		return 0, ErrChecksum
	}
	vals, err := decodePayload(h.enc, e.Baseline, h.count, block[h.headerLen:len(block)-crcLen])
	if err != nil {
		return 0, err
	}
	return vals[k-e.FirstIndex], nil
}
