package bitplane

import (
	"encoding/binary"
	"math/bits"
)

// Plane modes stored in the first byte of every plane record.
const (
	modeLiteral byte = 0 // ceil(n/8) packed bytes, LSB first
	modeRLE     byte = 1 // alternating run lengths starting with a zero run
	modeDelta   byte = 2 // deltas between positions of set bits, ascending
)

func uvarintLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

func chunkEnd(i, e int) int {
	j := (i/64 + 1) * 64
	if j > e {
		j = e
	}
	return j
}

// chunkWord returns the m=j-i (<=64) low-aligned bits covering [i,j).
func chunkWord(words []uint64, i, j int) uint64 {
	w := words[i/64] >> uint(i%64)
	if m := j - i; m < 64 {
		w &= (1 << uint(m)) - 1
	}
	return w
}

func onesMask(m int) uint64 {
	if m >= 64 {
		return ^uint64(0)
	}
	return (1 << uint(m)) - 1
}

// planeCost returns the minimum payload size and corresponding mode for the
// bit-plane slice [s,e). Plane record overhead is accounted by the caller.
func planeCost(words []uint64, s, e int) (int, byte) {
	n := e - s
	best := (n + 7) / 8
	mode := modeLiteral

	// Each delta needs at least one byte, so popcount is a cheap lower bound.
	if ones := popcountRange(words, s, e); ones < best {
		if ds := deltaSize(words, s, e); ds < best {
			best, mode = ds, modeDelta
		}
	}
	if rs := rleSize(words, s, e); rs < best {
		best, mode = rs, modeRLE
	}
	return best, mode
}

func popcountRange(words []uint64, s, e int) int {
	total := 0
	for i := s; i < e; {
		j := chunkEnd(i, e)
		total += bits.OnesCount64(chunkWord(words, i, j))
		i = j
	}
	return total
}

func deltaSize(words []uint64, s, e int) int {
	size := 0
	prev := 0
	for i := s; i < e; {
		j := chunkEnd(i, e)
		w := chunkWord(words, i, j)
		for w != 0 {
			pos := i + bits.TrailingZeros64(w) - s
			size += uvarintLen(uint64(pos - prev))
			prev = pos
			w &= w - 1
		}
		i = j
	}
	return size
}

// rleSize sums run-length varints; runs alternate starting with a possibly
// empty zero run, so an all-zero plane is a single tiny varint.
func rleSize(words []uint64, s, e int) int {
	size := 0
	cur, run := 0, 0
	flush := func() {
		size += uvarintLen(uint64(run))
		run = 0
	}
	for i := s; i < e; {
		j := chunkEnd(i, e)
		m := j - i
		w := chunkWord(words, i, j)
		switch {
		case w == 0:
			if cur == 1 {
				flush()
				cur = 0
			}
			run += m
		case w == onesMask(m):
			if cur == 0 {
				flush()
				cur = 1
			}
			run += m
		default:
			for k := 0; k < m; k++ {
				b := int((w >> uint(k)) & 1)
				if b == cur {
					run++
				} else {
					flush()
					cur, run = b, 1
				}
			}
		}
		i = j
	}
	flush()
	return size
}

// appendPlanePayload appends the mode-specific payload for plane slice [s,e).
func appendPlanePayload(buf []byte, words []uint64, s, e int, mode byte) []byte {
	n := e - s
	switch mode {
	case modeLiteral:
		base := len(buf)
		buf = append(buf, make([]byte, (n+7)/8)...)
		for k := 0; k < n; k++ {
			if (words[(s+k)>>6]>>uint((s+k)&63))&1 == 1 {
				buf[base+k>>3] |= 1 << uint(k&7)
			}
		}
	case modeRLE:
		cur, run := 0, 0
		flush := func() {
			buf = binary.AppendUvarint(buf, uint64(run))
			run = 0
		}
		for i := s; i < e; {
			j := chunkEnd(i, e)
			m := j - i
			w := chunkWord(words, i, j)
			switch {
			case w == 0:
				if cur == 1 {
					flush()
					cur = 0
				}
				run += m
			case w == onesMask(m):
				if cur == 0 {
					flush()
					cur = 1
				}
				run += m
			default:
				for k := 0; k < m; k++ {
					b := int((w >> uint(k)) & 1)
					if b == cur {
						run++
					} else {
						flush()
						cur, run = b, 1
					}
				}
			}
			i = j
		}
		flush()
	case modeDelta:
		prev := 0
		for i := s; i < e; {
			j := chunkEnd(i, e)
			w := chunkWord(words, i, j)
			for w != 0 {
				pos := i + bits.TrailingZeros64(w) - s
				buf = binary.AppendUvarint(buf, uint64(pos-prev))
				prev = pos
				w &= w - 1
			}
			i = j
		}
	}
	return buf
}

// decodePlane decodes a plane payload of n bits into out (a zeroed bitset).
// All length and position values are bounds-checked against n.
func decodePlane(payload []byte, mode byte, n int, out []uint64) error {
	set := func(k int) { out[k>>6] |= 1 << uint(k&63) }
	switch mode {
	case modeLiteral:
		if len(payload) != (n+7)/8 {
			return ErrFormat
		}
		for k := 0; k < n; k++ {
			if (payload[k>>3]>>uint(k&7))&1 == 1 {
				set(k)
			}
		}
	case modeRLE:
		pos, color := 0, 0
		i := 0
		for i < len(payload) {
			v, m := binary.Uvarint(payload[i:])
			if m <= 0 || v > uint64(n-pos) {
				return ErrFormat
			}
			i += m
			run := int(v)
			if color == 1 {
				for k := pos; k < pos+run; k++ {
					set(k)
				}
			}
			pos += run
			color ^= 1
		}
		if pos != n {
			return ErrFormat
		}
	case modeDelta:
		prev, i := 0, 0
		for i < len(payload) {
			v, m := binary.Uvarint(payload[i:])
			if m <= 0 || v > uint64(n) {
				return ErrFormat
			}
			i += m
			pos := prev + int(v)
			if pos >= n {
				return ErrFormat
			}
			set(pos)
			prev = pos
		}
	default:
		return ErrFormat
	}
	return nil
}
