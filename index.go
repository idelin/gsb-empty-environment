package blockstore

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Slice index encoding layout (the authoritative, serializable form of a
// slice's block chain — a plain byte string, not a map wrapper):
//
//	uvarint  format version (currently 1)
//	uvarint  logical length in bytes
//	uvarint  block count N
//	varint   block id[0]            (zigzag)
//	varint   block id[1]-id[0]      (zigzag delta)
//	...
//	varint   block id[N-1]-id[N-2]  (zigzag delta)
//
// Deltas are signed because reordered edits (e.g. Move) can make block ids
// non-monotonic. For append-only chains every delta is a small positive
// number, so each entry costs ~1 byte instead of 8.
const indexVersion = 1

var errBadIndex = errors.New("blockstore: corrupt index encoding")

func encodeIndex(length int, exts []uint64) []byte {
	buf := make([]byte, 0, 3+len(exts))
	buf = binary.AppendUvarint(buf, indexVersion)
	buf = binary.AppendUvarint(buf, uint64(length))
	buf = binary.AppendUvarint(buf, uint64(len(exts)))
	var prev uint64
	for i, id := range exts {
		if i == 0 {
			buf = binary.AppendVarint(buf, int64(id))
		} else {
			buf = binary.AppendVarint(buf, int64(id)-int64(prev))
		}
		prev = id
	}
	return buf
}

func decodeIndex(enc []byte) (length int, exts []uint64, err error) {
	rest := enc
	readU := func() (uint64, error) {
		v, n := binary.Uvarint(rest)
		if n <= 0 {
			return 0, errBadIndex
		}
		rest = rest[n:]
		return v, nil
	}
	readV := func() (int64, error) {
		v, n := binary.Varint(rest)
		if n <= 0 {
			return 0, errBadIndex
		}
		rest = rest[n:]
		return v, nil
	}
	ver, err := readU()
	if err != nil {
		return 0, nil, err
	}
	if ver != indexVersion {
		return 0, nil, fmt.Errorf("blockstore: unsupported index version %d", ver)
	}
	ln, err := readU()
	if err != nil {
		return 0, nil, err
	}
	count, err := readU()
	if err != nil {
		return 0, nil, err
	}
	exts = make([]uint64, 0, count)
	var prev uint64
	for i := uint64(0); i < count; i++ {
		d, err := readV()
		if err != nil {
			return 0, nil, err
		}
		var id uint64
		if i == 0 {
			if d < 0 {
				return 0, nil, errBadIndex
			}
			id = uint64(d)
		} else {
			id = uint64(int64(prev) + d)
		}
		exts = append(exts, id)
		prev = id
	}
	if len(rest) != 0 {
		return 0, nil, errBadIndex
	}
	return int(ln), exts, nil
}
