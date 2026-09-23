// Package wire defines the versioned, self-describing compressed file
// format used by dictcomp, together with an independent decoder.
//
// Compressed file format "DZC1" (version 1), all integers little-endian:
//
//	offset  size  field
//	0       4     magic "DZC1"
//	4       4     u32 dictionary entry count D (0..255)
//	8       ..    D entries: u16 length L followed by L raw bytes
//	..      8     u64 uncompressed length N
//	..      4     u32 CRC-32 (IEEE) of the uncompressed payload
//	..      4     u32 token stream length T in bytes
//	..      T     token stream
//
// Token stream: a sequence of tokens, each starting with a control byte c:
//
//	c == 0xFF   dictionary reference; the next byte is the dictionary index
//	0x01..0xFE  literal run of c bytes, followed by c literal bytes
//	0x00        reserved (invalid)
//
// The decoder in this file is independent of any compressor logic: it only
// needs the format description above.
package wire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

const (
	// MagicFile identifies a compressed file (format version 1).
	MagicFile = "DZC1"
	// MagicDict identifies a standalone dictionary file (version 1).
	MagicDict = "DZD1"

	// MaxDictEntries is the format limit: a reference token carries a
	// 1-byte dictionary index.
	MaxDictEntries = 255
	// MaxEntryLen is a sanity bound for a single dictionary entry.
	MaxEntryLen = 1024
	// LitRunMax is the longest literal run a single control byte encodes.
	LitRunMax = 254

	// fixedHeaderLen is magic + entry count + N + CRC + T.
	fixedHeaderLen = 4 + 4 + 8 + 4 + 4
)

var (
	ErrBadMagic  = errors.New("wire: bad magic")
	ErrBadCRC    = errors.New("wire: payload CRC mismatch")
	ErrTruncated = errors.New("wire: truncated input")
	ErrCorrupt   = errors.New("wire: corrupt stream")
	ErrTooLarge  = errors.New("wire: declared output exceeds limit")
)

// Token is one encoded unit: either a dictionary reference (Ref >= 0) or a
// literal byte run (Ref < 0, Lit holds the bytes).
type Token struct {
	Ref int
	Lit []byte
}

// FileOverhead returns the number of non-token bytes a file with the given
// dictionary carries: the fixed header plus the dictionary entries.
func FileOverhead(dict [][]byte) int64 {
	n := int64(fixedHeaderLen)
	for _, e := range dict {
		n += 2 + int64(len(e))
	}
	return n
}

// TokensSize returns the encoded size of a token stream in bytes.
func TokensSize(tokens []Token) int64 {
	var n int64
	for _, t := range tokens {
		if t.Ref >= 0 {
			n += 2
			continue
		}
		runs := int64(len(t.Lit)+LitRunMax-1) / LitRunMax
		if runs == 0 {
			runs = 1
		}
		n += runs + int64(len(t.Lit))
	}
	return n
}

// Encode serializes dict + tokens into the DZC1 format. plain is the
// uncompressed payload; it fills the length and CRC fields and is verified
// against the token stream.
func Encode(dict [][]byte, tokens []Token, plain []byte) ([]byte, error) {
	if len(dict) > MaxDictEntries {
		return nil, fmt.Errorf("wire: dictionary too large: %d entries", len(dict))
	}
	for _, e := range dict {
		if len(e) == 0 || len(e) > MaxEntryLen {
			return nil, fmt.Errorf("wire: bad dictionary entry length %d", len(e))
		}
	}
	if err := checkTokens(dict, tokens, plain); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.Grow(int(FileOverhead(dict)) + int(TokensSize(tokens)))
	buf.WriteString(MagicFile)
	writeU32(&buf, uint32(len(dict)))
	for _, e := range dict {
		writeU16(&buf, uint16(len(e)))
		buf.Write(e)
	}
	writeU64(&buf, uint64(len(plain)))
	writeU32(&buf, crc32.ChecksumIEEE(plain))
	writeU32(&buf, uint32(TokensSize(tokens)))
	for _, t := range tokens {
		if t.Ref >= 0 {
			buf.WriteByte(0xFF)
			buf.WriteByte(byte(t.Ref))
			continue
		}
		if len(t.Lit) == 0 {
			return nil, errors.New("wire: empty literal token")
		}
		for off := 0; off < len(t.Lit); off += LitRunMax {
			run := t.Lit[off:]
			if len(run) > LitRunMax {
				run = run[:LitRunMax]
			}
			buf.WriteByte(byte(len(run)))
			buf.Write(run)
		}
	}
	return buf.Bytes(), nil
}

// Decode parses a DZC1 file. maxOutput bounds the declared uncompressed
// length, so Decode never allocates more than maxOutput bytes of output.
func Decode(data []byte, maxOutput int64) (dict [][]byte, plain []byte, err error) {
	if len(data) < 8 || string(data[:4]) != MagicFile {
		return nil, nil, ErrBadMagic
	}
	r := &reader{buf: data, off: 4}
	count := r.u32()
	if r.err {
		return nil, nil, ErrTruncated
	}
	if count > MaxDictEntries {
		return nil, nil, fmt.Errorf("wire: dictionary count %d exceeds limit %d", count, MaxDictEntries)
	}
	for i := uint32(0); i < count; i++ {
		l := r.u16()
		b := r.take(int(l))
		if r.err {
			return nil, nil, ErrTruncated
		}
		if l == 0 || int(l) > MaxEntryLen {
			return nil, nil, fmt.Errorf("wire: bad dictionary entry length %d", l)
		}
		dict = append(dict, append([]byte(nil), b...))
	}
	n := r.u64()
	crc := r.u32()
	tlen := r.u32()
	if r.err {
		return nil, nil, ErrTruncated
	}
	if int64(n) > maxOutput {
		return nil, nil, ErrTooLarge
	}
	// A token produces at most MaxEntryLen bytes per 2 stream bytes, so a
	// declared N far beyond the stream size is impossible; reject before
	// allocating.
	if int64(n) > int64(tlen)/2*int64(MaxEntryLen)+LitRunMax {
		return nil, nil, fmt.Errorf("wire: declared length %d impossible for %d stream bytes", n, tlen)
	}
	stream := r.take(int(tlen))
	if r.err {
		return nil, nil, ErrTruncated
	}
	if r.off != len(data) {
		return nil, nil, fmt.Errorf("wire: %d trailing bytes after token stream", len(data)-r.off)
	}
	plain = make([]byte, 0, n)
	pos := 0
	for pos < len(stream) {
		c := stream[pos]
		pos++
		switch {
		case c == 0x00:
			return nil, nil, errors.New("wire: invalid control byte 0x00")
		case c == 0xFF:
			if pos >= len(stream) {
				return nil, nil, ErrTruncated
			}
			idx := int(stream[pos])
			pos++
			if idx >= len(dict) {
				return nil, nil, fmt.Errorf("wire: reference to missing dictionary entry %d", idx)
			}
			plain = append(plain, dict[idx]...)
		default:
			if pos+int(c) > len(stream) {
				return nil, nil, ErrTruncated
			}
			plain = append(plain, stream[pos:pos+int(c)]...)
			pos += int(c)
		}
		if uint64(len(plain)) > n {
			return nil, nil, fmt.Errorf("wire: decoded more than the declared %d bytes", n)
		}
	}
	if uint64(len(plain)) != n {
		return nil, nil, fmt.Errorf("wire: decoded %d bytes, header declares %d", len(plain), n)
	}
	if crc32.ChecksumIEEE(plain) != crc {
		return nil, nil, ErrBadCRC
	}
	return dict, plain, nil
}

// WriteDict serializes a dictionary into the DZD1 format:
//
//	"DZD1" u32 count, then per entry u16 length + bytes, then a u32 CRC-32
//	of everything after the magic.
func WriteDict(dict [][]byte) ([]byte, error) {
	if len(dict) > MaxDictEntries {
		return nil, fmt.Errorf("wire: dictionary too large: %d entries", len(dict))
	}
	var buf bytes.Buffer
	buf.WriteString(MagicDict)
	writeU32(&buf, uint32(len(dict)))
	for _, e := range dict {
		if len(e) == 0 || len(e) > MaxEntryLen {
			return nil, fmt.Errorf("wire: bad dictionary entry length %d", len(e))
		}
		writeU16(&buf, uint16(len(e)))
		buf.Write(e)
	}
	writeU32(&buf, crc32.ChecksumIEEE(buf.Bytes()[4:]))
	return buf.Bytes(), nil
}

// ReadDict parses a DZD1 dictionary file.
func ReadDict(data []byte) ([][]byte, error) {
	if len(data) < 12 || string(data[:4]) != MagicDict {
		return nil, ErrBadMagic
	}
	stored := binary.LittleEndian.Uint32(data[len(data)-4:])
	if crc32.ChecksumIEEE(data[4:len(data)-4]) != stored {
		return nil, ErrBadCRC
	}
	r := &reader{buf: data[:len(data)-4], off: 4}
	count := r.u32()
	if r.err || count > MaxDictEntries {
		return nil, ErrCorrupt
	}
	var dict [][]byte
	for i := uint32(0); i < count; i++ {
		l := r.u16()
		b := r.take(int(l))
		if r.err {
			return nil, ErrTruncated
		}
		if l == 0 || int(l) > MaxEntryLen {
			return nil, ErrCorrupt
		}
		dict = append(dict, append([]byte(nil), b...))
	}
	if r.off != len(data)-4 {
		return nil, ErrCorrupt
	}
	return dict, nil
}

// checkTokens verifies that the token stream reconstructs plain exactly.
func checkTokens(dict [][]byte, tokens []Token, plain []byte) error {
	off := 0
	for _, t := range tokens {
		var seg []byte
		if t.Ref >= 0 {
			if t.Ref >= len(dict) {
				return fmt.Errorf("wire: token references entry %d, dictionary has %d", t.Ref, len(dict))
			}
			seg = dict[t.Ref]
		} else {
			seg = t.Lit
		}
		if off+len(seg) > len(plain) || !bytes.Equal(plain[off:off+len(seg)], seg) {
			return errors.New("wire: token stream does not match payload")
		}
		off += len(seg)
	}
	if off != len(plain) {
		return errors.New("wire: token stream does not cover payload")
	}
	return nil
}

type reader struct {
	buf []byte
	off int
	err bool
}

func (r *reader) take(n int) []byte {
	if n < 0 || r.off+n > len(r.buf) {
		r.err = true
		return nil
	}
	b := r.buf[r.off : r.off+n]
	r.off += n
	return b
}

func (r *reader) u16() uint16 {
	b := r.take(2)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint16(b)
}

func (r *reader) u32() uint32 {
	b := r.take(4)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}

func (r *reader) u64() uint64 {
	b := r.take(8)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint64(b)
}

func writeU16(buf *bytes.Buffer, v uint16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	buf.Write(b[:])
}

func writeU32(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

func writeU64(buf *bytes.Buffer, v uint64) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	buf.Write(b[:])
}
