package adaptivehuffman

import "fmt"

// ErrorKind classifies the errors produced by this package.
type ErrorKind int

const (
	// ErrKindBadMagic indicates a frame whose magic bytes are not "AHF1".
	ErrKindBadMagic ErrorKind = iota + 1
	// ErrKindBadVersion indicates an unsupported frame/state version.
	ErrKindBadVersion
	// ErrKindTruncated indicates the bit stream ended in the middle of a
	// codeword (or of the 8 raw bits following an escape, or of the header).
	ErrKindTruncated
	// ErrKindInvalidEscape indicates an escape (NYT) codeword that is not
	// legal at this point: either all 256 symbols are already known, or the
	// raw byte following the escape already has a codeword.
	ErrKindInvalidEscape
	// ErrKindNonZeroPadding indicates a non-zero bit in the zero padding
	// region at the end of a frame.
	ErrKindNonZeroPadding
	// ErrKindTrailingData indicates extra bytes after the end of a frame.
	ErrKindTrailingData
	// ErrKindCorruptState indicates an invalid exported-state blob.
	ErrKindCorruptState
)

func (k ErrorKind) String() string {
	switch k {
	case ErrKindBadMagic:
		return "bad magic"
	case ErrKindBadVersion:
		return "bad version"
	case ErrKindTruncated:
		return "truncated codeword"
	case ErrKindInvalidEscape:
		return "invalid escape"
	case ErrKindNonZeroPadding:
		return "non-zero padding bits"
	case ErrKindTrailingData:
		return "trailing data after frame"
	case ErrKindCorruptState:
		return "corrupt state"
	}
	return "unknown error"
}

// Error is the error type returned by this package. Byte and Bit locate the
// offending position: Bit is the bit offset inside Byte, with 0 denoting the
// most significant bit. For errors reported by DecodeFrame, Byte is relative
// to the start of the frame (header included). For errors reported by
// Decoder.DecodeSymbol on a raw BitReader, Byte is relative to the start of
// the BitReader's underlying stream.
type Error struct {
	Kind ErrorKind
	Byte int64
	Bit  uint8
}

func (e *Error) Error() string {
	return fmt.Sprintf("adaptivehuffman: %s at byte %d, bit %d", e.Kind, e.Byte, e.Bit)
}
