package prefixcode

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
)

// Wire format (all integers big-endian):
//
//	offset 0   3 bytes  magic "PCD"
//	offset 3   1 byte   format version (= Version)
//	offset 4   1 byte   maxLen: maximum code length in bits
//	offset 5   3 bytes  reserved, must be zero
//	offset 8   256 bytes code length per symbol (0 = symbol unused)
//	offset 264 8 bytes  numSymbols: number of encoded symbols
//	offset 272 8 bytes  payloadBytes: number of encoded payload bytes
//	offset 280 4 bytes  CRC-32 (IEEE) of offset 0..279 and the payload
//	offset 284          payload bits, MSB-first, final byte zero-padded
//
// The CRC covers the code table and both length fields, so any tampering
// with the table, the declared lengths, or the payload is detected
// deterministically instead of being misdecoded.
const (
	// Version is the current format version.
	Version    = 1
	headerSize = 284
	numSymbols = 256
)

const maxInt64 = int64(^uint64(0) >> 1)

// Compress builds optimal code lengths for message (bounded by maxLen) and
// returns the message in the wire format.
func Compress(message []byte, maxLen int) ([]byte, error) {
	var freqs [numSymbols]uint64
	for _, b := range message {
		freqs[b]++
	}
	lengths, err := BuildLengths(freqs[:], maxLen)
	if err != nil {
		return nil, err
	}
	return Marshal(lengths, message)
}

// Marshal encodes message with the given code lengths (one per byte value)
// into the wire format. Oversubscribed lengths are rejected.
func Marshal(lengths []int, message []byte) ([]byte, error) {
	if len(lengths) != numSymbols {
		return nil, fmt.Errorf("%v: want %d code lengths, got %d", ErrFormat, numSymbols, len(lengths))
	}
	maxLen := 0
	for _, l := range lengths {
		if l < 0 || l > MaxMaxLen {
			return nil, fmt.Errorf("%v: length %d", ErrBadLength, l)
		}
		if l > maxLen {
			maxLen = l
		}
	}
	codes, err := CanonicalCodes(lengths)
	if err != nil {
		return nil, err
	}
	payload, err := Encode(message, codes)
	if err != nil {
		return nil, err
	}
	out := make([]byte, headerSize, headerSize+len(payload))
	out[0], out[1], out[2] = 'P', 'C', 'D'
	out[3] = Version
	out[4] = byte(maxLen)
	for i, l := range lengths {
		out[8+i] = byte(l)
	}
	binary.BigEndian.PutUint64(out[264:], uint64(len(message)))
	binary.BigEndian.PutUint64(out[272:], uint64(len(payload)))
	h := crc32.NewIEEE()
	h.Write(out[:280])
	h.Write(payload)
	binary.BigEndian.PutUint32(out[280:], h.Sum32())
	return append(out, payload...), nil
}

// Decompress reads one wire-format stream from r and decodes it. Reading
// and decoding are incremental, so r may deliver data in arbitrary chunks.
// quota bounds the total decoded output (negative = unlimited); if the
// declared message size exceeds quota, ErrQuota is returned before any
// decoding. Exactly the declared payload is consumed from r; any bytes
// following the stream are left unread.
func Decompress(r io.Reader, quota int64) ([]byte, error) {
	hdr := make([]byte, headerSize)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, fmt.Errorf("%v: header: %s", ErrTruncated, err)
	}
	if hdr[0] != 'P' || hdr[1] != 'C' || hdr[2] != 'D' {
		return nil, fmt.Errorf("%v: bad magic", ErrFormat)
	}
	if hdr[3] != Version {
		return nil, fmt.Errorf("%v: unsupported version %d", ErrFormat, hdr[3])
	}
	maxLen := int(hdr[4])
	if maxLen > MaxMaxLen {
		return nil, fmt.Errorf("%v: maxLen %d", ErrFormat, maxLen)
	}
	lengths := make([]int, numSymbols)
	for i := range lengths {
		l := int(hdr[8+i])
		if l > maxLen {
			return nil, fmt.Errorf("%v: length %d exceeds maxLen %d", ErrFormat, l, maxLen)
		}
		lengths[i] = l
	}
	numSym := binary.BigEndian.Uint64(hdr[264:])
	payLen := binary.BigEndian.Uint64(hdr[272:])
	wantCRC := binary.BigEndian.Uint32(hdr[280:])
	if payLen > uint64(maxInt64)/8 || numSym > payLen*8 {
		return nil, fmt.Errorf("%v: %d symbols cannot fit in %d payload bytes", ErrFormat, numSym, payLen)
	}
	if numSym > uint64(maxInt64) {
		return nil, fmt.Errorf("%v: symbol count too large", ErrFormat)
	}
	if quota >= 0 && uint64(quota) < numSym {
		return nil, ErrQuota
	}
	tbl, err := NewTable(lengths)
	if err != nil {
		return nil, err
	}
	h := crc32.NewIEEE()
	h.Write(hdr[:280])
	dec := NewDecoder(tbl, WithQuota(quota), WithSymbolCount(int64(numSym)))
	var out []byte
	buf := make([]byte, 8192)
	feed := func(p []byte, finish bool) error {
		for {
			c, n, err := dec.Decode(p, buf, finish)
			out = append(out, buf[:n]...)
			p = p[c:]
			if err != nil {
				return err
			}
			if c == 0 && n == 0 {
				return nil
			}
		}
	}
	chunk := make([]byte, 32*1024)
	left := int64(payLen)
	for left > 0 {
		n := int64(len(chunk))
		if left < n {
			n = left
		}
		m, err := io.ReadFull(r, chunk[:int(n)])
		if err != nil {
			return nil, fmt.Errorf("%v: payload: %s", ErrTruncated, err)
		}
		h.Write(chunk[:m])
		if err := feed(chunk[:m], false); err != nil {
			return nil, err
		}
		left -= int64(m)
	}
	if err := feed(nil, true); err != nil {
		return nil, err
	}
	if h.Sum32() != wantCRC {
		return nil, ErrChecksum
	}
	return out, nil
}
