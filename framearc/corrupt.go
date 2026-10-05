package framearc

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// The Corrupt* helpers build damaged archives for testing and for the
// CLI. None of them are used by the reader itself.

// InsertBytes returns a copy of arc with junk inserted at off.
func InsertBytes(arc []byte, off int, junk []byte) []byte {
	if off < 0 {
		off = 0
	}
	if off > len(arc) {
		off = len(arc)
	}
	out := make([]byte, 0, len(arc)+len(junk))
	out = append(out, arc[:off]...)
	out = append(out, junk...)
	out = append(out, arc[off:]...)
	return out
}

// DropBytes returns a copy of arc with n bytes removed starting at off.
func DropBytes(arc []byte, off, n int) []byte {
	if off < 0 {
		off = 0
	}
	if off > len(arc) {
		off = len(arc)
	}
	if off+n > len(arc) {
		n = len(arc) - off
	}
	out := make([]byte, 0, len(arc)-n)
	out = append(out, arc[:off]...)
	out = append(out, arc[off+n:]...)
	return out
}

// FlipBit returns a copy of arc with one bit flipped at byte off.
func FlipBit(arc []byte, off int, bit uint) []byte {
	out := append([]byte(nil), arc...)
	if off >= 0 && off < len(out) {
		out[off] ^= 1 << bit
	}
	return out
}

// FakeMagic returns bytes that look like a frame start but are not:
// the magic followed by zero fields, which fail structural validation.
func FakeMagic() []byte {
	return append([]byte(Magic), make([]byte, 12)...)
}

// WrongTable returns a copy of arc in which the code table of frame
// frameIdx is replaced by a different but still valid table, with the
// header CRC recomputed. The frame stays structurally sound, so a
// reader can trust its extent, but its payload no longer decodes to the
// original bytes.
func WrongTable(arc []byte, frameIdx int) ([]byte, error) {
	spans, err := ParseArchive(arc)
	if err != nil {
		return nil, err
	}
	if frameIdx < 0 || frameIdx >= len(spans) {
		return nil, errors.New("framearc: frame index out of range")
	}
	sp := spans[frameIdx]
	h, err := parseHeader(arc[sp.Start:])
	if err != nil {
		return nil, err
	}
	out := append([]byte(nil), arc...)
	tab := sp.Start + fixedHeaderLen
	n := len(h.table)
	// Prefer swapping the code lengths of two entries with different
	// lengths: the table stays canonical but assigns different codes.
	i, j := -1, -1
	for a := 0; a < n && j < 0; a++ {
		for b := a + 1; b < n; b++ {
			if h.table[a][1] != h.table[b][1] {
				i, j = a, b
				break
			}
		}
	}
	if j >= 0 {
		out[tab+2*i+1], out[tab+2*j+1] = out[tab+2*j+1], out[tab+2*i+1]
	} else {
		// All lengths equal L: bump the first entry to L+1. The table
		// remains a valid (incomplete) canonical code.
		if h.table[0][1] >= maxCodeLen {
			return nil, errors.New("framearc: cannot corrupt table")
		}
		out[tab+1] = h.table[0][1] + 1
	}
	crcOff := sp.Start + h.headerLen - headerCRCLen
	binary.BigEndian.PutUint32(out[crcOff:], crc32.ChecksumIEEE(out[sp.Start:crcOff]))
	return out, nil
}
