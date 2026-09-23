package lpc

import (
	"encoding/binary"
	"hash/crc32"
)

// Full message layout (header followed by one frame):
//
//	code header                (see Marshal, includes its own CRC)
//	4 bytes                    message symbol count M, big-endian
//	k bytes                    MSB-first bit-packed canonical code words,
//	                           byte aligned, zero padding
//	4 bytes                    CRC-32/IEEE over (count bytes || k payload)
//
// Decoding requires an explicit output quota maxOut: frames whose symbol
// count exceeds it are rejected before any allocation, so compressed input
// can never force unbounded decoded output.

// Encode packs a slice of symbol ids into one self-describing message.
func (c *Code) Encode(symbols []int) ([]byte, error) {
	if c == nil {
		return nil, ErrBadArgument
	}
	n := len(c.Lengths)
	if uint64(len(symbols)) > 0xffffffff {
		return nil, ErrBadArgument
	}
	hdr, err := c.Marshal()
	if err != nil {
		return nil, err
	}

	w := newBitWriter()
	for _, s := range symbols {
		if s < 0 || s >= n {
			return nil, ErrBadArgument
		}
		l := c.Lengths[s]
		if l > 0 { // l == 0 only for the one-symbol empty code word
			w.write(c.words[s], uint(l))
		}
	}
	payload := w.bytes()

	frame := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(symbols)))
	copy(frame[4:], payload)

	out := make([]byte, 0, len(hdr)+len(frame)+4)
	out = append(out, hdr...)
	out = append(out, frame...)
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], crc32.ChecksumIEEE(frame))
	out = append(out, sum[:]...)
	return out, nil
}

// Decode parses a message produced by Encode. maxOut bounds the number of
// decoded symbols; the message is rejected with ErrOutputLimit if it claims
// more, and corrupt input is rejected with ErrBadHeader, ErrChecksum or
// ErrInvalidBitstream.
func (c *Code) Decode(data []byte, maxOut int) ([]int, error) {
	if c == nil || maxOut < 0 {
		return nil, ErrBadArgument
	}
	n := len(c.Lengths)
	hdrLen := 11 + n + 4
	if len(data) < hdrLen {
		return nil, ErrBadHeader
	}
	if _, err := UnmarshalCode(data[:hdrLen]); err != nil {
		return nil, err
	}
	rest := data[hdrLen:]
	if len(rest) < 8 {
		return nil, ErrInvalidBitstream
	}
	count := int(binary.BigEndian.Uint32(rest[0:4]))
	if count > maxOut {
		return nil, ErrOutputLimit
	}
	payloadEnd := len(rest) - 4
	frame := rest[:payloadEnd]
	payload := frame[4:]
	stored := binary.BigEndian.Uint32(rest[payloadEnd:])
	if crc32.ChecksumIEEE(frame) != stored {
		return nil, ErrChecksum
	}

	// Length -> map of code word -> symbol; only lengths actually used.
	byLength := make(map[int]map[uint64]int)
	var lengths []int
	seen := make(map[int]bool)
	for s, l := range c.Lengths {
		if m := byLength[l]; m == nil {
			byLength[l] = map[uint64]int{c.words[s]: s}
		} else {
			m[c.words[s]] = s
		}
		if !seen[l] {
			seen[l] = true
			lengths = append(lengths, l)
		}
	}

	out := make([]int, 0, count)
	br := newBitReader(payload)

	if n == 1 {
		// Empty code words: no bits are consumed.
		if len(payload) != 0 {
			return nil, ErrInvalidBitstream
		}
		for i := 0; i < count; i++ {
			out = append(out, 0)
		}
		return out, nil
	}

	for i := 0; i < count; i++ {
		var code uint64
		matched := false
		for depth := 1; depth <= c.MaxLen; depth++ {
			bit, err := br.readBit()
			if err != nil {
				return nil, ErrInvalidBitstream
			}
			code = (code << 1) | uint64(bit)
			if m := byLength[depth]; m != nil {
				if s, ok := m[code]; ok {
					if len(out) >= maxOut {
						return nil, ErrOutputLimit
					}
					out = append(out, s)
					matched = true
					break
				}
			}
		}
		if !matched {
			return nil, ErrInvalidBitstream
		}
	}

	// All payload bits must be consumed; trailing padding must be zero.
	if br.consumedOrNonZeroPadding() {
		return nil, ErrInvalidBitstream
	}
	return out, nil
}

// bitWriter packs bits MSB first.
type bitWriter struct {
	buf  []byte
	cur  byte
	used uint // bits used in cur (0..7)
}

func newBitWriter() *bitWriter { return &bitWriter{} }

func (w *bitWriter) write(v uint64, nbits uint) {
	for i := int(nbits) - 1; i >= 0; i-- {
		if v&(uint64(1)<<uint(i)) != 0 {
			w.cur |= 1 << (7 - w.used)
		}
		w.used++
		if w.used == 8 {
			w.buf = append(w.buf, w.cur)
			w.cur = 0
			w.used = 0
		}
	}
}

func (w *bitWriter) bytes() []byte {
	if w.used > 0 {
		return append(append([]byte(nil), w.buf...), w.cur)
	}
	return append([]byte(nil), w.buf...)
}

// bitReader unpacks MSB-first bits.
type bitReader struct {
	buf     []byte
	byteIdx int
	bitIdx  uint // next bit position within the byte, 0..7
}

func newBitReader(buf []byte) *bitReader { return &bitReader{buf: buf} }

func (r *bitReader) readBit() (int, error) {
	if r.byteIdx >= len(r.buf) {
		return 0, ErrInvalidBitstream
	}
	b := int((r.buf[r.byteIdx] >> (7 - r.bitIdx)) & 1)
	r.bitIdx++
	if r.bitIdx == 8 {
		r.bitIdx = 0
		r.byteIdx++
	}
	return b, nil
}

// consumedOrNonZeroPadding reports true if unread bits remain and any of
// them (the padding within the last partially used byte, or extra bytes)
// is non-zero. It returns false only when the reader sits exactly on a byte
// boundary, or the remaining padding is all zero.
func (r *bitReader) consumedOrNonZeroPadding() bool {
	if r.bitIdx == 0 {
		return r.byteIdx != len(r.buf)
	}
	// Current byte: remaining high? bits after bitIdx must be zero.
	if r.buf[r.byteIdx]&(byte(1)<<(8-r.bitIdx)-1) != 0 {
		return true
	}
	for i := r.byteIdx + 1; i < len(r.buf); i++ {
		if r.buf[i] != 0 {
			return true
		}
	}
	return false
}
