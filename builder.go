package cidx

import (
	"bytes"
	"encoding/binary"
	"math"
)

// Builder encodes a compact index from strictly increasing keys.
type Builder struct {
	keys   [][]byte
	values [][]byte
}

// Add appends one entry. Keys must be added in strictly increasing
// lexicographic order.
func (b *Builder) Add(key, value []byte) error {
	if len(b.keys) > 0 && bytes.Compare(b.keys[len(b.keys)-1], key) >= 0 {
		return ErrBuilderKeyOrder
	}
	b.keys = append(b.keys, append([]byte(nil), key...))
	b.values = append(b.values, append([]byte(nil), value...))
	return nil
}

// Bytes encodes the index and returns the exact occupied bytes.
func (b *Builder) Bytes() ([]byte, error) {
	n := len(b.keys)
	if n >= math.MaxUint32 {
		return nil, ErrTooLarge
	}
	var data []byte
	offsets := make([]uint32, n+1)
	for i := range b.keys {
		if uint64(len(data)) > math.MaxUint32 {
			return nil, ErrTooLarge
		}
		offsets[i] = uint32(len(data))
		data = appendRecord(data, b.keys[i], b.values[i])
	}
	if uint64(len(data)) > math.MaxUint32 {
		return nil, ErrTooLarge
	}
	offsets[n] = uint32(len(data))

	tableOffset := uint64(HeaderSize)
	dataOffset := tableOffset + uint64(n+1)*offsetEntrySize
	h := header{
		entryCount:  uint32(n),
		tableOffset: tableOffset,
		dataOffset:  dataOffset,
		dataLength:  uint64(len(data)),
	}
	out := encodeHeader(h)
	var tmp [offsetEntrySize]byte
	for _, o := range offsets {
		binary.LittleEndian.PutUint32(tmp[:], o)
		out = append(out, tmp[:]...)
	}
	return append(out, data...), nil
}
