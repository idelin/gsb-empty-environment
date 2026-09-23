package rangecoder

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// Container layout (all integers big-endian):
//
//	[0:3]  magic "RAC"
//	[3]    format version (1)
//	[4:12] uint64: length of the original payload in bytes
//	[12:16] uint32: CRC-32 (IEEE) of the original payload
//	[16:]  range-coded symbols, terminated by the EOF symbol
const (
	formatVersion = 1
	headerSize    = 3 + 1 + 8 + 4
)

var magic = []byte{'R', 'A', 'C'}

var (
	ErrTruncated  = errors.New("rangecoder: input shorter than header")
	ErrBadMagic   = errors.New("rangecoder: bad magic bytes")
	ErrBadVersion = errors.New("rangecoder: unsupported format version")
	ErrBudget     = errors.New("rangecoder: output budget exceeded")
	ErrLength     = errors.New("rangecoder: decoded length mismatch")
	ErrChecksum   = errors.New("rangecoder: checksum mismatch")
)

// Encode compresses src into the self-describing container format.
func Encode(src []byte) []byte {
	var out bytes.Buffer
	out.Grow(headerSize + len(src)/2 + 16)
	out.Write(magic)
	out.WriteByte(formatVersion)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(len(src)))
	out.Write(buf[:])
	binary.BigEndian.PutUint32(buf[:4], crc32.ChecksumIEEE(src))
	out.Write(buf[:4])

	m := newModel()
	e := newEncoder(&out)
	for _, b := range src {
		sym := uint32(b)
		e.encode(m.cumFreq(sym), m.freq[sym], m.total)
		m.update(sym)
	}
	// Terminate the stream explicitly.
	e.encode(m.cumFreq(eofSymbol), m.freq[eofSymbol], m.total)
	e.flush()
	return out.Bytes()
}

// Decode expands a container produced by Encode. maxOutput is a hard cap on
// the number of decoded bytes: decoding stops with ErrBudget as soon as the
// cap would be exceeded, so corrupt or hostile input can never cause
// unbounded work or memory growth. maxOutput must be >= the original length.
func Decode(data []byte, maxOutput int) ([]byte, error) {
	if len(data) < headerSize {
		return nil, ErrTruncated
	}
	if !bytes.Equal(data[:3], magic) {
		return nil, ErrBadMagic
	}
	if data[3] != formatVersion {
		return nil, ErrBadVersion
	}
	wantLen := binary.BigEndian.Uint64(data[4:12])
	wantCRC := binary.BigEndian.Uint32(data[12:16])
	if maxOutput < 0 || wantLen > uint64(maxOutput) {
		return nil, ErrBudget
	}

	// Preallocate modestly; a corrupt header must not cause a huge alloc.
	capHint := wantLen
	if capHint > 1<<20 {
		capHint = 1 << 20
	}
	out := make([]byte, 0, capHint)

	m := newModel()
	d := newDecoder(data[headerSize:])
	for {
		sym, cum := m.find(d.cumFreq(m.total))
		d.update(cum, m.freq[sym], m.total)
		if sym == eofSymbol {
			break
		}
		if len(out) >= maxOutput {
			return nil, ErrBudget
		}
		out = append(out, byte(sym))
		m.update(sym)
	}
	if uint64(len(out)) != wantLen {
		return nil, ErrLength
	}
	if crc32.ChecksumIEEE(out) != wantCRC {
		return nil, ErrChecksum
	}
	return out, nil
}
