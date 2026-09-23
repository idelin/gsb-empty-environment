package huffcodec

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// Wire format (all integers unsigned, big-endian unless noted):
//
//	byte  0-1 : magic 'H','C'
//	byte  2   : format version (FormatVersion)
//	uvarint   : number of symbols with a code (nSym)
//	nSym * 2  : (symbol byte, code length byte) pairs, sorted by symbol
//	uvarint   : uncompressed length in bytes
//	4 bytes   : CRC-32 (IEEE) of the uncompressed data
//	rest      : canonical-code bitstream, MSB first, zero-padded tail
const FormatVersion = 1

var formatMagic = [2]byte{'H', 'C'}

var (
	ErrBadVersion   = errors.New("huffcodec: unsupported format version")
	ErrCorrupt      = errors.New("huffcodec: corrupt or truncated header")
	ErrChecksum     = errors.New("huffcodec: checksum mismatch")
	ErrTruncated    = errors.New("huffcodec: truncated payload")
	ErrTrailingData = errors.New("huffcodec: trailing data after payload")
)

// Encode compresses data into the self-describing wire format using an
// exactly optimal prefix code limited to maxBits per code. Empty input is
// legal and produces a header-only stream.
func Encode(data []byte, maxBits int) ([]byte, error) {
	var freqs [256]uint64
	for _, b := range data {
		freqs[b]++
	}
	lengths, err := CodeLengths(freqs[:], maxBits)
	if err != nil {
		return nil, err
	}
	out := []byte{formatMagic[0], formatMagic[1], FormatVersion}
	nSym := 0
	for _, l := range lengths {
		if l > 0 {
			nSym++
		}
	}
	out = binary.AppendUvarint(out, uint64(nSym))
	for sym, l := range lengths {
		if l > 0 {
			out = append(out, byte(sym), l)
		}
	}
	out = binary.AppendUvarint(out, uint64(len(data)))
	var crcb [4]byte
	binary.BigEndian.PutUint32(crcb[:], crc32.ChecksumIEEE(data))
	out = append(out, crcb[:]...)
	if nSym == 0 {
		return out, nil
	}
	codes, err := CanonicalCodes(lengths)
	if err != nil {
		return nil, err
	}
	bw := bitWriter{buf: out}
	for _, b := range data {
		c := codes[b]
		bw.writeBits(c.Bits, uint(c.Len))
	}
	bw.flush()
	return bw.buf, nil
}

// DecodeAll decodes a complete wire-format stream. maxOut bounds the
// declared uncompressed size (the decode output quota); pass a negative
// value for unlimited. Version, code table validity (oversubscribed or
// incomplete tables are rejected), declared length, padding and checksum
// are all verified, so a truncated tail or tampered table never reports
// success.
func DecodeAll(enc []byte, maxOut int64) ([]byte, error) {
	if len(enc) < 3 {
		return nil, ErrCorrupt
	}
	if enc[0] != formatMagic[0] || enc[1] != formatMagic[1] {
		return nil, ErrCorrupt
	}
	if enc[2] != FormatVersion {
		return nil, ErrBadVersion
	}
	rest := enc[3:]
	nSym64, n := binary.Uvarint(rest)
	if n <= 0 || nSym64 > 256 {
		return nil, ErrCorrupt
	}
	rest = rest[n:]
	if uint64(len(rest)) < nSym64*2 {
		return nil, ErrCorrupt
	}
	var lengths [256]uint8
	for i := uint64(0); i < nSym64; i++ {
		sym, l := rest[0], rest[1]
		rest = rest[2:]
		if l == 0 || l > MaxCodeLength || lengths[sym] != 0 {
			return nil, ErrCorrupt
		}
		lengths[sym] = l
	}
	outLen, n := binary.Uvarint(rest)
	if n <= 0 {
		return nil, ErrCorrupt
	}
	rest = rest[n:]
	if len(rest) < 4 {
		return nil, ErrCorrupt
	}
	wantCRC := binary.BigEndian.Uint32(rest[:4])
	payload := rest[4:]
	if maxOut >= 0 && int64(outLen) > maxOut {
		return nil, ErrQuotaExceeded
	}
	if nSym64 == 0 {
		if outLen != 0 || len(payload) != 0 {
			return nil, ErrCorrupt
		}
		if crc32.ChecksumIEEE(nil) != wantCRC {
			return nil, ErrChecksum
		}
		return []byte{}, nil
	}
	if err := ValidateLengths(lengths[:]); err != nil {
		return nil, err
	}
	dec, err := NewStreamDecoder(lengths[:], int64(outLen))
	if err != nil {
		return nil, err
	}
	out := make([]byte, outLen)
	consumed, produced, err := dec.Decode(payload, out)
	if err != nil {
		return nil, err
	}
	if uint64(produced) < outLen {
		// Truncated tail: classify as mid-codeword when possible.
		if ferr := dec.Finish(); ferr != nil {
			return nil, ferr
		}
		return nil, ErrTruncated
	}
	if consumed != len(payload) {
		return nil, ErrTrailingData
	}
	// Any remaining buffered bits must be zero padding of the final byte.
	if dec.nbits >= 8 || dec.acc != 0 {
		return nil, ErrTrailingData
	}
	if crc32.ChecksumIEEE(out) != wantCRC {
		return nil, ErrChecksum
	}
	return out, nil
}
