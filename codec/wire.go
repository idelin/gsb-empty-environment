// Package codec implements a schema-driven binary serialization format
// with stable field numbers, unknown-field passthrough, version
// diagnostics, and allocation-safe decoding.
//
// Supported serialization scope (deliberately small and total):
//   - unsigned/signed integers, booleans, enums (varint)
//   - 32/64-bit floats and fixed-width integers (fixed32/fixed64)
//   - strings, byte blobs, and nested messages (length-delimited)
//   - repeated fields of any of the above
//
// Out of scope by design: maps, sets, self-describing type tags,
// cyclic references, and any form of embedded code or type URLs.
// Decoding never executes host code, never uses reflection, and never
// dispatches on data-supplied type names.
package codec

import (
	"encoding/binary"
	"errors"
)

const (
	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
	wireFixed32 = 5
)

// Hard limits enforced before any allocation happens during decoding.
const (
	// MaxFieldNumber is the largest legal field number (29-bit, like protobuf).
	MaxFieldNumber = 1<<29 - 1
	// MaxFieldSize bounds a single length-delimited payload.
	MaxFieldSize = 1 << 20 // 1 MiB
	// MaxFields bounds the number of fields in one message.
	MaxFields = 1 << 16
	// MaxMessageSize bounds a whole encoded message.
	MaxMessageSize = 64 << 20 // 64 MiB
)

var (
	ErrTruncated   = errors.New("codec: truncated input")
	ErrOverflow    = errors.New("codec: varint overflow")
	ErrTooLarge    = errors.New("codec: declared length exceeds limits")
	ErrBadWireType = errors.New("codec: unsupported wire type")
)

func appendTag(dst []byte, num, wire int) []byte {
	return binary.AppendUvarint(dst, uint64(num)<<3|uint64(wire))
}

// readUvarint consumes a varint at *off, advancing the offset. It never
// allocates and reports truncation/overflow instead of looping forever.
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

// readLen reads a declared length and validates it against both the
// configured cap and the bytes actually remaining, before the caller
// allocates anything.
func readLen(buf []byte, off *int) (int, error) {
	n, err := readUvarint(buf, off)
	if err != nil {
		return 0, err
	}
	if n > MaxFieldSize {
		return 0, ErrTooLarge
	}
	if n > uint64(len(buf)-*off) {
		return 0, ErrTruncated
	}
	return int(n), nil
}
