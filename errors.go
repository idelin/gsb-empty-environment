package controlflow

import "errors"

// Sentinel errors produced by the engine itself. A decoder may wrap these or
// supply its own error values.
var (
	// ErrShortRead means fewer bytes were available than the instruction
	// required, so its length could not be determined.
	ErrShortRead = errors.New("controlflow: short read while decoding instruction")
	// ErrZeroLength is recorded when a decoder reports a non-positive length
	// without supplying its own error.
	ErrZeroLength = errors.New("controlflow: decoder reported zero-length instruction")
	// ErrNoDecoder is returned when Engine.Decoder is nil.
	ErrNoDecoder = errors.New("controlflow: engine requires an injected decoder")
	// ErrNoEntry is returned when the trusted entry set is empty.
	ErrNoEntry = errors.New("controlflow: recovery requires at least one trusted entry")
)

// IsShortRead reports whether err is (or wraps) ErrShortRead.
func IsShortRead(err error) bool { return errors.Is(err, ErrShortRead) }
