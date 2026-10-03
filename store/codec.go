// Package store implements the compact binary persistence format for the
// Multi-Paxos log, including versioned encoding, bounds-checked decoding and
// zero-copy restoration from memory-mapped segments.
package store

import (
	"encoding/binary"
	"errors"
)

// Format versions. V1 is the original layout; V2 adds the Flags field.
// Decoders accept any payload version: known fields are parsed by prefix and
// unknown trailing fields are skipped via the record length frame, which
// gives both backward (new reader / old file) and forward (old reader / new
// file) compatibility.
const (
	FormatV1 uint16 = 1
	FormatV2 uint16 = 2

	// CurrentFormat is the version written by this build.
	CurrentFormat = FormatV2
)

// Hard limits that guard every length-prefixed field against overflow and
// corrupt-length amplification attacks.
const (
	MaxCommandSize = 1 << 20 // 1 MiB per command
	MaxRecordSize  = 4 << 20 // 4 MiB per framed record
)

var (
	ErrCorrupt   = errors.New("store: corrupt record")
	ErrChecksum  = errors.New("store: checksum mismatch")
	ErrTruncated = errors.New("store: truncated data")
	ErrTooLarge  = errors.New("store: record exceeds size limit")
	ErrOverflow  = errors.New("store: varint overflow")
)

// Entry is a single replicated log record.
type Entry struct {
	Slot    uint64
	Term    uint64
	NodeID  uint32
	Flags   uint32 // V2+; zero when decoded from V1 payloads
	Command []byte
}

// IsZero reports whether e is an empty placeholder.
func (e Entry) IsZero() bool {
	return e.Slot == 0 && e.Term == 0 && e.NodeID == 0 && e.Flags == 0 && len(e.Command) == 0
}

// payloadSize returns the encoded payload size of e at the given version.
func payloadSize(e Entry, version uint16) int {
	n := 1 // version byte
	n += uvarintLen(e.Slot)
	n += uvarintLen(e.Term)
	n += uvarintLen(uint64(e.NodeID))
	if version >= FormatV2 {
		n += uvarintLen(uint64(e.Flags))
	}
	n += uvarintLen(uint64(len(e.Command)))
	n += len(e.Command)
	return n
}

func uvarintLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

// AppendEncode appends the versioned payload encoding of e to dst and returns
// the extended slice. It panics if the command exceeds MaxCommandSize.
func AppendEncode(dst []byte, e Entry, version uint16) []byte {
	if len(e.Command) > MaxCommandSize {
		panic("store: command exceeds MaxCommandSize")
	}
	if version < FormatV1 || version > CurrentFormat {
		panic("store: unsupported encode version")
	}
	dst = append(dst, byte(version))
	dst = binary.AppendUvarint(dst, e.Slot)
	dst = binary.AppendUvarint(dst, e.Term)
	dst = binary.AppendUvarint(dst, uint64(e.NodeID))
	if version >= FormatV2 {
		dst = binary.AppendUvarint(dst, uint64(e.Flags))
	}
	dst = binary.AppendUvarint(dst, uint64(len(e.Command)))
	return append(dst, e.Command...)
}

// Encode returns a freshly allocated payload encoding of e.
func Encode(e Entry, version uint16) []byte {
	return AppendEncode(make([]byte, 0, payloadSize(e, version)), e, version)
}

// readUvarint decodes a uvarint from buf[*off:], advancing *off.
func readUvarint(buf []byte, off *int) (uint64, error) {
	if *off >= len(buf) {
		return 0, ErrTruncated
	}
	v, n := binary.Uvarint(buf[*off:])
	if n == 0 {
		return 0, ErrTruncated
	}
	if n < 0 {
		return 0, ErrOverflow
	}
	*off += n
	return v, nil
}

// DecodeEntry decodes a payload produced by AppendEncode. The returned
// Entry's Command aliases payload (zero-copy); callers must not mutate the
// underlying buffer while holding the Entry. Unknown trailing bytes (from
// newer format versions) are ignored; missing trailing fields (from older
// versions) default to zero.
func DecodeEntry(payload []byte) (Entry, error) {
	var e Entry
	if len(payload) == 0 {
		return e, ErrTruncated
	}
	version := uint16(payload[0])
	if version < FormatV1 {
		return e, ErrCorrupt
	}
	off := 1
	var err error
	if e.Slot, err = readUvarint(payload, &off); err != nil {
		return Entry{}, err
	}
	if e.Term, err = readUvarint(payload, &off); err != nil {
		return Entry{}, err
	}
	node, err := readUvarint(payload, &off)
	if err != nil {
		return Entry{}, err
	}
	if node > 0xffffffff {
		return Entry{}, ErrOverflow
	}
	e.NodeID = uint32(node)
	if version >= FormatV2 {
		flags, err := readUvarint(payload, &off)
		if err != nil {
			return Entry{}, err
		}
		if flags > 0xffffffff {
			return Entry{}, ErrOverflow
		}
		e.Flags = uint32(flags)
	}
	cmdLen, err := readUvarint(payload, &off)
	if err != nil {
		return Entry{}, err
	}
	if cmdLen > MaxCommandSize {
		return Entry{}, ErrTooLarge
	}
	if cmdLen > uint64(len(payload)-off) {
		return Entry{}, ErrTruncated
	}
	// Zero-copy: Command aliases the caller's buffer.
	e.Command = payload[off : off+int(cmdLen) : off+int(cmdLen)]
	return e, nil
}
