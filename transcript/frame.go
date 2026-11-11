package transcript

import (
	"errors"
	"fmt"
)

// Field type tags. Every field frame is self-describing, so the framing
// itself, not the byte content, determines field boundaries.
const (
	typeBytes uint64 = 1
	typeUint  uint64 = 2
	typeInt   uint64 = 3
)

// endTag marks the closing frame of a transcript. Application fields must
// use tags strictly greater than endTag.
const endTag uint64 = 0

// MaxFieldLen bounds the payload of a single field and therefore the size
// of any untrusted frame the parser will accept.
const MaxFieldLen = 16 << 20 // 16 MiB

var (
	errEmptyFrame   = errors.New("transcript: empty frame")
	errTrailingData = errors.New("transcript: trailing bytes after frame")
)

// appendFrame appends one canonical field frame:
//
//	phase || tag || type || len(payload) || payload
//
// where every header integer is a minimal varint. The explicit length
// means payload bytes never need to be escaped and two distinct field
// sequences can never produce the same byte stream.
func appendFrame(dst []byte, phase, tag, typ uint64, payload []byte) []byte {
	dst = appendUvarint(dst, phase)
	dst = appendUvarint(dst, tag)
	dst = appendUvarint(dst, typ)
	dst = appendUvarint(dst, uint64(len(payload)))
	return append(dst, payload...)
}

// frame is the parsed view of one canonical field frame.
type frame struct {
	phase   uint64
	tag     uint64
	typ     uint64
	payload []byte
}

// parseFrame parses exactly one canonical frame from raw. It enforces
// minimal varint encodings, known type codes, the tag range and the field
// length limit, and rejects any trailing bytes so callers cannot feed two
// frames (or padding) where one is expected.
func parseFrame(raw []byte) (frame, error) {
	if len(raw) == 0 {
		return frame{}, errEmptyFrame
	}

	phase, n, err := readUvarint(raw)
	if err != nil {
		return frame{}, fmt.Errorf("transcript: phase: %w", err)
	}
	tag, m, err := readUvarint(raw[n:])
	if err != nil {
		return frame{}, fmt.Errorf("transcript: tag: %w", err)
	}
	n += m
	typ, m, err := readUvarint(raw[n:])
	if err != nil {
		return frame{}, fmt.Errorf("transcript: type: %w", err)
	}
	n += m
	length, m, err := readUvarint(raw[n:])
	if err != nil {
		return frame{}, fmt.Errorf("transcript: length: %w", err)
	}
	n += m

	switch typ {
	case typeBytes, typeUint, typeInt:
	default:
		return frame{}, fmt.Errorf("transcript: unknown field type %d", typ)
	}
	if tag <= endTag {
		return frame{}, errors.New("transcript: frame tag must be greater than zero")
	}
	if length > MaxFieldLen {
		return frame{}, fmt.Errorf("transcript: field length %d exceeds %d", length, MaxFieldLen)
	}
	if uint64(len(raw)-n) < length {
		return frame{}, errors.New("transcript: truncated frame payload")
	}
	n += int(length)
	if n != len(raw) {
		return frame{}, errTrailingData
	}

	payload := make([]byte, length)
	copy(payload, raw[n-int(length):])
	return frame{phase: phase, tag: tag, typ: typ, payload: payload}, nil
}
