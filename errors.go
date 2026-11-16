package netsec

import "errors"

// MLLP framing bytes, as defined by the MLLP profile (HL7 v2, chapter on the
// minimal lower layer protocol).
const (
	VT byte = 0x0B // vertical tab: frame start
	FS byte = 0x1C // file separator: first byte of the frame trailer
	CR byte = 0x0D // carriage return: second byte of the trailer / segment end
)

// Sentinel errors returned by the package. All are wrapped with context, so
// callers should use errors.Is rather than direct equality.
var (
	// ErrProtocol is a fatal framing violation that makes the byte stream
	// untrustworthy: a byte outside a frame that is not VT, or a second VT
	// encountered while a frame is already open. The connection must be
	// closed; the parser becomes permanently unusable until Reset.
	ErrProtocol = errors.New("mllp: protocol error")

	// ErrBadTrailer is returned when an FS is not immediately followed by a
	// CR. The offending frame is discarded and the parser resynchronizes at
	// the next VT; the transport itself is not torn down.
	ErrBadTrailer = errors.New("mllp: frame trailer FS not followed by CR")

	// ErrFrameTooLarge is returned when a frame payload exceeds the
	// configured limit. The oversized frame is discarded and bytes are
	// skipped until the next VT.
	ErrFrameTooLarge = errors.New("mllp: frame payload exceeds limit")

	// ErrFramingByte is returned by the writer or field validators when an
	// MLLP framing byte (VT or FS) appears where it cannot be represented
	// safely.
	ErrFramingByte = errors.New("mllp: payload contains framing byte VT(0x0B) or FS(0x1C)")

	// ErrCRPosition is returned by the writer when a CR is not used as a
	// segment terminator: every CR must be the final payload byte or the
	// byte immediately preceding the next segment identifier.
	ErrCRPosition = errors.New("mllp: CR(0x0D) is only allowed at the end of a segment")

	// ErrEscape is returned by the HL7 unescaper for an unterminated or
	// unrecognized escape sequence, and by field validators for a decoded
	// framing byte.
	ErrEscape = errors.New("mllp: invalid or forbidden HL7 escape sequence")
)
