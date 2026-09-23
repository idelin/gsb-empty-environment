package bitplane

import (
	"encoding/binary"
	"fmt"
)

// Plane chunk modes.
const (
	modeLiteral = 0 // raw packed bits
	modeRLE     = 1 // alternating run lengths, starting with a zero-length zero run
	modeDelta   = 1 << 1
)

func uvarintSize(x uint64) int {
	n := 1
	for x >= 0x80 {
		x >>= 7
		n++
	}
	return n
}

func literalPayloadSize(bl int) int { return (bl + 7) / 8 }

// rlePayloadSize returns the encoded size of the plane chunk using
// alternating zero/one run lengths, each stored as a uvarint.
func rlePayloadSize(plane []uint64, start, bl int) int {
	size, run, cur := 0, 0, uint64(0)
	for i := 0; i < bl; i++ {
		if b := bitAt(plane, start+i); b == cur {
			run++
		} else {
			size += uvarintSize(uint64(run))
			run, cur = 1, b
		}
	}
	return size + uvarintSize(uint64(run))
}

// deltaPayloadSize returns the encoded size using a count of set bits
// followed by uvarint gaps between their positions.
func deltaPayloadSize(plane []uint64, start, bl int) int {
	size, prev, first := 0, 0, true
	k := 0
	for i := 0; i < bl; i++ {
		if bitAt(plane, start+i) == 1 {
			if first {
				size += uvarintSize(uint64(i))
				first = false
			} else {
				size += uvarintSize(uint64(i - prev - 1))
			}
			prev = i
			k++
		}
	}
	return uvarintSize(uint64(k)) + size
}

// bestPlaneMode picks the cheapest mode for one plane chunk by real payload
// size (the caller adds one byte for the mode tag itself).
func bestPlaneMode(plane []uint64, start, bl int) (int, int) {
	best, bestSize := modeLiteral, literalPayloadSize(bl)
	if s := rlePayloadSize(plane, start, bl); s < bestSize {
		best, bestSize = modeRLE, s
	}
	if s := deltaPayloadSize(plane, start, bl); s < bestSize {
		best, bestSize = modeDelta, s
	}
	return best, bestSize
}

func appendLiteral(dst []byte, plane []uint64, start, bl int) []byte {
	var b byte
	for i := 0; i < bl; i++ {
		if bitAt(plane, start+i) == 1 {
			b |= 1 << uint(i&7)
		}
		if i&7 == 7 {
			dst = append(dst, b)
			b = 0
		}
	}
	if bl&7 != 0 {
		dst = append(dst, b)
	}
	return dst
}

func appendRLE(dst []byte, plane []uint64, start, bl int) []byte {
	var tmp [binary.MaxVarintLen64]byte
	run, cur := 0, uint64(0)
	emit := func(r int) {
		n := binary.PutUvarint(tmp[:], uint64(r))
		dst = append(dst, tmp[:n]...)
	}
	for i := 0; i < bl; i++ {
		if b := bitAt(plane, start+i); b == cur {
			run++
		} else {
			emit(run)
			run, cur = 1, b
		}
	}
	emit(run)
	return dst
}

func appendDelta(dst []byte, plane []uint64, start, bl int) []byte {
	var tmp [binary.MaxVarintLen64]byte
	emit := func(x uint64) {
		n := binary.PutUvarint(tmp[:], x)
		dst = append(dst, tmp[:n]...)
	}
	k := 0
	for i := 0; i < bl; i++ {
		if bitAt(plane, start+i) == 1 {
			k++
		}
	}
	emit(uint64(k))
	prev, first := 0, true
	for i := 0; i < bl; i++ {
		if bitAt(plane, start+i) == 1 {
			if first {
				emit(uint64(i))
				first = false
			} else {
				emit(uint64(i - prev - 1))
			}
			prev = i
		}
	}
	return dst
}

func readUvarint(src []byte) (uint64, int, error) {
	v, n := binary.Uvarint(src)
	if n <= 0 {
		return 0, 0, fmt.Errorf("bitplane: malformed varint")
	}
	return v, n, nil
}

// decodeLiteral unpacks bl bits from src, returning bytes consumed.
func decodeLiteral(src []byte, pl []uint64, bl int) (int, error) {
	need := literalPayloadSize(bl)
	if len(src) < need {
		return 0, fmt.Errorf("bitplane: literal plane truncated: need %d bytes, have %d", need, len(src))
	}
	for i := 0; i < bl; i++ {
		if src[i>>3]>>uint(i&7)&1 == 1 {
			setBit(pl, i)
		}
	}
	return need, nil
}

// decodeRLE decodes alternating zero/one runs until exactly bl bits are
// produced; the run total may never exceed the block length.
func decodeRLE(src []byte, pl []uint64, bl int) (int, error) {
	pos, cur, off, runs := 0, 0, 0, 0
	for pos < bl {
		run, n, err := readUvarint(src[off:])
		if err != nil {
			return 0, err
		}
		off += n
		runs++
		if runs > bl+1 {
			return 0, fmt.Errorf("bitplane: rle run count exceeds block length %d", bl)
		}
		if run > uint64(bl-pos) {
			return 0, fmt.Errorf("bitplane: rle run %d overruns block length %d", run, bl)
		}
		if cur == 1 {
			for i := pos; i < pos+int(run); i++ {
				setBit(pl, i)
			}
		}
		pos += int(run)
		cur ^= 1
	}
	return off, nil
}

// decodeDelta decodes a set-bit count followed by position gaps.
func decodeDelta(src []byte, pl []uint64, bl int) (int, error) {
	k, n, err := readUvarint(src)
	if err != nil {
		return 0, err
	}
	off := n
	if k > uint64(bl) {
		return 0, fmt.Errorf("bitplane: delta count %d exceeds block length %d", k, bl)
	}
	prev := -1
	for i := 0; i < int(k); i++ {
		g, n, err := readUvarint(src[off:])
		if err != nil {
			return 0, err
		}
		off += n
		pos := prev + 1 + int(g)
		if i == 0 {
			pos = int(g)
		}
		if pos <= prev || pos >= bl {
			return 0, fmt.Errorf("bitplane: delta position %d out of order or beyond block length %d", pos, bl)
		}
		setBit(pl, pos)
		prev = pos
	}
	return off, nil
}
