package netsec

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"io"
)

// Frame layout of a compressed status list on the wire:
//
//	offset 0: 8 bytes, big-endian uint64, the *declared* uncompressed length
//	          of the bitmap (the number of bytes that were compressed).
//	offset 8: the remaining bytes, a ZLIB [RFC 1950] data stream carrying a
//	          DEFLATE [RFC 1951] compressed bitmap, as produced by the Token
//	          Status List encoding.
//
// The declared length prefix is the untrusted, attacker-controlled value that
// lets a tiny compressed payload claim a huge expansion ("decompression bomb").
// It is therefore validated against MaxDeclaredSize before any byte of the
// compressed stream is inflated.
const LengthPrefixSize = 8

// MaxDeclaredSize is the largest declared uncompressed length that the decoder
// is willing to inflate. Any frame whose declared length exceeds this value is
// rejected before decompression starts, so a malicious frame can never force
// the decoder to allocate an enormous buffer.
const MaxDeclaredSize = 1 << 20 // 1 MiB, comfortably above any real bitmap.

var (
	// ErrShortFrame is returned when the frame is too short to hold the
	// 8-byte declared-length prefix.
	ErrShortFrame = errors.New("netsec: compressed status list frame is too short")
	// ErrDeclaredTooLarge is returned when the declared uncompressed length is
	// greater than MaxDeclaredSize. This is checked before decompression.
	ErrDeclaredTooLarge = errors.New("netsec: declared uncompressed length exceeds limit")
	// ErrLengthMismatch is returned when the number of bytes actually produced
	// by decompression does not equal the declared uncompressed length.
	ErrLengthMismatch = errors.New("netsec: decompressed length does not match declared length")
	// ErrMalformed is returned when the ZLIB stream cannot be fully read.
	ErrMalformed = errors.New("netsec: malformed compressed status list")
)

// DecodeStatusList validates and inflates a framed compressed status list.
//
// It enforces, in order:
//  1. the frame carries an 8-byte declared-length prefix;
//  2. the declared length is within MaxDeclaredSize (rejecting an oversized
//     declaration before allocating or inflating anything);
//  3. the ZLIB stream inflates without error and is well formed;
//  4. the inflated length is exactly the declared length.
//
// On success it returns the decompressed bitmap. The returned error is one of
// the package's sentinel errors (possibly wrapped) and is never nil on failure.
func DecodeStatusList(frame []byte) ([]byte, error) {
	if len(frame) < LengthPrefixSize {
		return nil, ErrShortFrame
	}

	declared := binary.BigEndian.Uint64(frame[:LengthPrefixSize])
	if declared > MaxDeclaredSize {
		// Fail fast, before touching the compressed payload.
		return nil, ErrDeclaredTooLarge
	}

	rc, err := zlib.NewReader(bytes.NewReader(frame[LengthPrefixSize:]))
	if err != nil {
		return nil, errors.Join(ErrMalformed, err)
	}

	// Read at most declared+1 bytes. Capping the read keeps memory bounded even
	// if the declared check were ever bypassed and lets us detect an over-long
	// stream through the final length comparison.
	limited := io.LimitReader(rc, int64(declared)+1)
	bitmap, readErr := io.ReadAll(limited)
	closeErr := rc.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(ErrMalformed, readErr, closeErr)
	}

	if uint64(len(bitmap)) != declared {
		return nil, ErrLengthMismatch
	}

	return bitmap, nil
}

// BitAt returns the value of the 1-bit status entry at the given index in the
// decompressed bitmap. Bits are numbered from the least-significant bit of
// byte 0, matching the Token Status List bit ordering. It returns an error when
// index falls outside the bitmap.
func BitAt(bitmap []byte, index int) (int, error) {
	if index < 0 || index/8 >= len(bitmap) {
		return 0, ErrLengthMismatch
	}
	return int((bitmap[index/8] >> (uint(index) % 8)) & 1), nil
}
