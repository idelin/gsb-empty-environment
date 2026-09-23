// Package framecomp implements a frame-independent compressed container.
//
// Frame layout (all integers little-endian):
//
//	magic    4 bytes  "FCMP"
//	version  1 byte   format version (currently 1)
//	method   1 byte   0 = store, 1 = LZ77
//	compLen  uint32   compressed payload length in bytes
//	origLen  uint32   original (decompressed) length in bytes
//	crc32    uint32   IEEE CRC-32 of the complete original payload
//	payload  compLen bytes
//
// Frames are self-delimiting and independently decodable, so a decoder can
// resynchronize on the magic marker after corrupted or foreign bytes.
package framecomp

import "errors"

// ErrMalformed reports structurally invalid compressed token streams.
var ErrMalformed = errors.New("framecomp: malformed compressed data")

// ErrTooMuchOutput reports decompression exceeding the caller's output cap.
var ErrTooMuchOutput = errors.New("framecomp: decompressed size exceeds limit")

const (
	minMatch   = 3
	maxMatch   = 130 // 3 + 127, encodable in 7 bits
	windowSize = 65535
	hashBits   = 15
	hashSize   = 1 << hashBits
	maxLitRun  = 128
)

func hash3(b []byte, i int) uint32 {
	v := uint32(b[i])<<16 | uint32(b[i+1])<<8 | uint32(b[i+2])
	return v * 2654435761 >> (32 - hashBits)
}

// lzCompress encodes src as a token stream. Token format:
//   - ctrl < 128: literal run of ctrl+1 bytes follows.
//   - ctrl >= 128: match of (ctrl-128)+minMatch bytes, followed by a
//     uint16 little-endian backward distance.
func lzCompress(src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	out := make([]byte, 0, len(src)/2+16)
	head := make([]int, hashSize)
	for i := range head {
		head[i] = -1
	}
	litStart := 0
	flushLit := func(end int) {
		for litStart < end {
			n := end - litStart
			if n > maxLitRun {
				n = maxLitRun
			}
			out = append(out, byte(n-1))
			out = append(out, src[litStart:litStart+n]...)
			litStart += n
		}
	}
	i := 0
	for i+minMatch <= len(src) {
		h := hash3(src, i)
		cand := head[h]
		head[h] = i
		matchLen := 0
		if cand >= 0 && i-cand <= windowSize {
			max := len(src) - i
			if max > maxMatch {
				max = maxMatch
			}
			n := 0
			for n < max && src[cand+n] == src[i+n] {
				n++
			}
			if n >= minMatch {
				matchLen = n
			}
		}
		if matchLen >= minMatch {
			flushLit(i)
			dist := i - cand
			out = append(out, byte(128+matchLen-minMatch), byte(dist), byte(dist>>8))
			end := i + matchLen
			for j := i + 1; j < end && j+minMatch <= len(src); j++ {
				head[hash3(src, j)] = j
			}
			i = end
			litStart = i
		} else {
			i++
		}
	}
	flushLit(len(src))
	return out
}

// lzDecompress decodes a token stream produced by lzCompress. The output is
// capped at maxOut bytes; exceeding the cap or any structural defect returns
// an error and no partial result is usable by the caller.
func lzDecompress(src []byte, maxOut int) ([]byte, error) {
	out := make([]byte, 0, maxOut)
	i := 0
	for i < len(src) {
		ctrl := src[i]
		i++
		if ctrl < 128 {
			n := int(ctrl) + 1
			if i+n > len(src) {
				return nil, ErrMalformed
			}
			if len(out)+n > maxOut {
				return nil, ErrTooMuchOutput
			}
			out = append(out, src[i:i+n]...)
			i += n
			continue
		}
		n := int(ctrl-128) + minMatch
		if i+2 > len(src) {
			return nil, ErrMalformed
		}
		dist := int(src[i]) | int(src[i+1])<<8
		i += 2
		if dist == 0 || dist > len(out) {
			return nil, ErrMalformed
		}
		if len(out)+n > maxOut {
			return nil, ErrTooMuchOutput
		}
		start := len(out) - dist
		for k := 0; k < n; k++ {
			out = append(out, out[start+k])
		}
	}
	return out, nil
}
