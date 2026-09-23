package prefixcode

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// Stream format (all integers big-endian):
//
//	magic      [4]byte  "PCD1"  (format identifier + version 1)
//	maxLen     uint32           maximum code length L
//	numSymbols uint32           symbol count n
//	lengths    [n]uint8         code length per symbol, in symbol order
//	payloadBits uint64          number of valid bits in payload
//	payload    []byte           ceil(payloadBits/8) bytes, MSB-first
//	checksum   uint32           CRC-32 (IEEE) of all preceding bytes
//
// The version, every length field, and the checksum are explicit, so a
// decoder can reject unknown versions, malformed lengths, and corruption
// before emitting any output.
const Magic = "PCD1"

const (
	headerLen  = 4 + 4 + 4 // magic + maxLen + numSymbols
	trailerLen = 4         // crc32
)

// Encode serializes the code table and the encoded symbol stream.
func Encode(c *Code, symbols []int) ([]byte, error) {
	bw := &bitWriter{}
	for _, s := range symbols {
		code, length, err := c.Bits(s)
		if err != nil {
			return nil, err
		}
		bw.writeBits(code, length)
	}
	payload := bw.finish()

	var buf bytes.Buffer
	buf.WriteString(Magic)
	writeU32(&buf, uint32(c.MaxLen))
	writeU32(&buf, uint32(len(c.Lengths)))
	buf.Write(c.Lengths)
	writeU64(&buf, bw.total)
	buf.Write(payload)
	writeU32(&buf, crc32.ChecksumIEEE(buf.Bytes()))
	return buf.Bytes(), nil
}

// Decode parses a stream produced by Encode and returns the code table and
// the decoded symbols.
//
// maxSymbols bounds the decoded output: if the payload could expand to more
// than maxSymbols symbols, Decode fails before (or as soon as) the bound is
// exceeded. Pass maxSymbols < 0 for no bound.
func Decode(data []byte, maxSymbols int) (*Code, []int, error) {
	if len(data) < headerLen+trailerLen {
		return nil, nil, errors.New("prefixcode: stream too short")
	}
	if string(data[:4]) != Magic {
		return nil, nil, fmt.Errorf("prefixcode: unsupported format version %q", data[:4])
	}
	maxLen := int(binary.BigEndian.Uint32(data[4:8]))
	numSym := int(binary.BigEndian.Uint32(data[8:12]))
	if maxLen < 1 || maxLen > MaxLimit {
		return nil, nil, fmt.Errorf("prefixcode: bad maxLen %d", maxLen)
	}
	if numSym < 1 || uint64(numSym) > uint64(1)<<uint(maxLen) {
		return nil, nil, fmt.Errorf("prefixcode: %d symbols cannot fit in 2^%d codewords", numSym, maxLen)
	}
	if uint64(len(data)) < uint64(headerLen)+uint64(numSym)+8+trailerLen {
		return nil, nil, errors.New("prefixcode: stream truncated in header")
	}
	lengths := data[headerLen : headerLen+numSym]
	off := headerLen + numSym
	payloadBits := binary.BigEndian.Uint64(data[off : off+8])
	off += 8

	// Bounded output: every symbol costs at least 1 and at most maxLen
	// bits, so payloadBits > maxSymbols*maxLen guarantees overflow.
	if maxSymbols >= 0 && payloadBits > uint64(maxSymbols)*uint64(maxLen) {
		return nil, nil, fmt.Errorf("prefixcode: payload of %d bits exceeds decode limit of %d symbols", payloadBits, maxSymbols)
	}
	payloadLen := (payloadBits + 7) / 8
	if uint64(len(data)-off-trailerLen) != payloadLen {
		return nil, nil, fmt.Errorf("prefixcode: payload length mismatch, want %d bytes", payloadLen)
	}
	payload := data[off : off+int(payloadLen)]
	wantCRC := binary.BigEndian.Uint32(data[len(data)-trailerLen:])
	if got := crc32.ChecksumIEEE(data[:len(data)-trailerLen]); got != wantCRC {
		return nil, nil, fmt.Errorf("prefixcode: checksum mismatch (got %08x, want %08x)", got, wantCRC)
	}

	code, err := NewFromLengths(lengths, maxLen)
	if err != nil {
		return nil, nil, err
	}
	symbols, err := code.decodePayload(payload, payloadBits, maxSymbols)
	if err != nil {
		return nil, nil, err
	}
	return code, symbols, nil
}

// decodePayload decodes exactly nbits bits from payload using canonical
// codes, honoring the maxSymbols output bound.
func (c *Code) decodePayload(payload []byte, nbits uint64, maxSymbols int) ([]int, error) {
	// Lookup table keyed by (length << 32) | code.
	type key struct {
		length int
		code   uint32
	}
	table := make(map[key]int, len(c.Lengths))
	for sym := range c.Lengths {
		table[key{int(c.Lengths[sym]), c.codes[sym]}] = sym
	}
	var out []int
	var cur uint32
	var curlen int
	for i := uint64(0); i < nbits; i++ {
		bit := uint32(payload[i/8] >> (7 - uint(i%8)) & 1)
		cur = cur<<1 | bit
		curlen++
		if sym, ok := table[key{curlen, cur}]; ok {
			out = append(out, sym)
			if maxSymbols >= 0 && len(out) > maxSymbols {
				return nil, fmt.Errorf("prefixcode: decode output exceeds limit of %d symbols", maxSymbols)
			}
			cur, curlen = 0, 0
		}
	}
	if curlen != 0 {
		return nil, errors.New("prefixcode: payload ends mid-codeword")
	}
	return out, nil
}

func writeU32(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

func writeU64(buf *bytes.Buffer, v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	buf.Write(b[:])
}

// bitWriter accumulates bits MSB-first.
type bitWriter struct {
	buf   []byte
	cur   byte
	nbits uint
	total uint64
}

func (w *bitWriter) writeBits(code uint32, length int) {
	for i := length - 1; i >= 0; i-- {
		w.cur = w.cur<<1 | byte(code>>uint(i)&1)
		w.nbits++
		if w.nbits == 8 {
			w.buf = append(w.buf, w.cur)
			w.cur, w.nbits = 0, 0
		}
	}
	w.total += uint64(length)
}

func (w *bitWriter) finish() []byte {
	if w.nbits > 0 {
		w.buf = append(w.buf, w.cur<<(8-w.nbits))
	}
	return w.buf
}
