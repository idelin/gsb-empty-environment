package dzblock

import "encoding/binary"

// A small LZ77 compressor/decompressor. Matches may reach back into the
// shared dictionary, which is treated as a read-only prefix of every
// block. Tokens are grouped in eights under one control byte; a set bit
// marks a match (2-byte little-endian offset, 1-byte length-minMatch),
// a clear bit marks a literal byte.

const (
	minMatch   = 4
	maxMatch   = minMatch + 255 // length stored in one byte
	windowSize = 1 << 16
	maxOffset  = windowSize - 1
	hashBits   = 15
	hashSize   = 1 << hashBits
	maxChain   = 128
)

func hash4(b []byte, i int) int {
	v := binary.LittleEndian.Uint32(b[i:])
	return int((v * 2654435761) >> (32 - hashBits))
}

// lzEncode compresses block, allowing matches into dict and into the
// already-encoded prefix of block itself.
func lzEncode(dst, dict, block []byte) []byte {
	src := make([]byte, 0, len(dict)+len(block))
	src = append(src, dict...)
	src = append(src, block...)
	base := len(dict)
	n := len(src)

	head := make([]int, hashSize)
	for i := range head {
		head[i] = -1
	}
	prev := make([]int, windowSize)
	insert := func(i int) {
		if i+minMatch > n {
			return
		}
		h := hash4(src, i)
		prev[i&(windowSize-1)] = head[h]
		head[h] = i
	}
	// Index the dictionary so blocks can match against it.
	for i := 0; i+minMatch <= base; i++ {
		insert(i)
	}

	ctrlPos := len(dst)
	dst = append(dst, 0)
	var ctrl byte
	var nbits uint
	flushGroup := func() {
		dst[ctrlPos] = ctrl
		ctrlPos = len(dst)
		dst = append(dst, 0)
		ctrl, nbits = 0, 0
	}
	emitLiteral := func(b byte) {
		dst = append(dst, b)
		nbits++
		if nbits == 8 {
			flushGroup()
		}
	}
	emitMatch := func(off, l int) {
		dst = append(dst, byte(off), byte(off>>8), byte(l-minMatch))
		ctrl |= 1 << nbits
		nbits++
		if nbits == 8 {
			flushGroup()
		}
	}

	pos := base
	for pos < n {
		bestLen, bestOff := 0, 0
		if pos+minMatch <= n {
			limit := pos - maxOffset
			if limit < 0 {
				limit = 0
			}
			maxLen := n - pos
			if maxLen > maxMatch {
				maxLen = maxMatch
			}
			cand := head[hash4(src, pos)]
			for chain := 0; cand >= limit && chain < maxChain; chain++ {
				l := 0
				for l < maxLen && src[cand+l] == src[pos+l] {
					l++
				}
				if l > bestLen {
					bestLen, bestOff = l, pos-cand
					if l >= maxLen {
						break
					}
				}
				cand = prev[cand&(windowSize-1)]
			}
		}
		if bestLen >= minMatch {
			emitMatch(bestOff, bestLen)
			for k := 0; k < bestLen; k++ {
				insert(pos + k)
			}
			pos += bestLen
		} else {
			emitLiteral(src[pos])
			insert(pos)
			pos++
		}
	}
	dst[ctrlPos] = ctrl
	if nbits == 0 && len(dst) > 0 {
		dst = dst[:len(dst)-1] // drop the unused trailing control byte
	}
	return dst
}

// lzDecode decodes payload into exactly rawLen bytes appended to dst,
// using dict as the read-only prefix window. Output is strictly bounded
// by rawLen; any malformed token is rejected.
func lzDecode(dst, dict, payload []byte, rawLen int) ([]byte, error) {
	out := dst
	base := len(dst)
	produced := 0
	i := 0
	for produced < rawLen {
		if i >= len(payload) {
			return nil, ErrCorrupt
		}
		ctrl := payload[i]
		i++
		for bit := uint(0); bit < 8 && produced < rawLen; bit++ {
			if ctrl&(1<<bit) == 0 {
				if i >= len(payload) {
					return nil, ErrCorrupt
				}
				out = append(out, payload[i])
				i++
				produced++
				continue
			}
			if i+3 > len(payload) {
				return nil, ErrCorrupt
			}
			off := int(payload[i]) | int(payload[i+1])<<8
			l := int(payload[i+2]) + minMatch
			i += 3
			if off < 1 || off > produced+len(dict) {
				return nil, ErrCorrupt
			}
			if produced+l > rawLen {
				return nil, ErrCorrupt
			}
			for k := 0; k < l; k++ {
				idx := produced - off
				var b byte
				if idx >= 0 {
					b = out[base+idx]
				} else {
					di := len(dict) + idx
					if di < 0 {
						return nil, ErrCorrupt
					}
					b = dict[di]
				}
				out = append(out, b)
				produced++
			}
		}
	}
	return out, nil
}
