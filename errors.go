package prefixcode

import "errors"

var (
	// ErrOversubscribed: Kraft sum of the code lengths exceeds 1.
	ErrOversubscribed = errors.New("prefixcode: code lengths oversubscribed (Kraft sum > 1)")
	// ErrIncomplete: Kraft sum of the code lengths is below 1.
	ErrIncomplete = errors.New("prefixcode: code table incomplete (Kraft sum < 1)")
	// ErrBadLength: a code length is negative or exceeds the allowed maximum.
	ErrBadLength = errors.New("prefixcode: invalid code length")
	// ErrMaxLen: max code length out of range, or too small for the alphabet.
	ErrMaxLen = errors.New("prefixcode: max code length out of range or too small")
	// ErrInvalidCode: bit pattern matches no codeword (unknown or overlong code).
	ErrInvalidCode = errors.New("prefixcode: invalid or overlong code")
	// ErrTruncated: input ended in the middle of a codeword or message.
	ErrTruncated = errors.New("prefixcode: truncated input, incomplete codeword")
	// ErrTrailingData: nonzero or oversized padding after the final symbol.
	ErrTrailingData = errors.New("prefixcode: trailing data after message")
	// ErrQuota: the decoder output budget is exhausted.
	ErrQuota = errors.New("prefixcode: output quota exhausted")
	// ErrShortBuffer: the caller-provided output slice is full.
	ErrShortBuffer = errors.New("prefixcode: output buffer too small")
	// ErrFormat: header fields (magic, version, lengths, sizes) are invalid.
	ErrFormat = errors.New("prefixcode: bad format header")
	// ErrChecksum: CRC-32 over header and payload does not match.
	ErrChecksum = errors.New("prefixcode: checksum mismatch")
	// ErrNoCode: attempted to encode a symbol that has no assigned code.
	ErrNoCode = errors.New("prefixcode: symbol has no code")
)
