package sctpaddr

import "errors"

// Hard rejection errors returned by Dispatch. These describe packets that are
// dropped without an ASCONF ACK: a malformed packet, a failing verification
// tag, a missing/bad AUTH HMAC, or a sequence number out of the strict window.
var (
	ErrMalformed      = errors.New("sctpaddr: malformed packet")
	ErrBadTag         = errors.New("sctpaddr: verification tag mismatch")
	ErrMissingAUTH    = errors.New("sctpaddr: ASCONF without AUTH chunk")
	ErrBadHMAC        = errors.New("sctpaddr: AUTH HMAC verification failed")
	ErrUnknownHMAC    = errors.New("sctpaddr: unsupported HMAC identifier")
	ErrReplayedASCONF = errors.New("sctpaddr: replayed ASCONF sequence number")
	ErrStaleASCONF    = errors.New("sctpaddr: stale ASCONF sequence number")
	ErrFutureASCONF   = errors.New("sctpaddr: ASCONF sequence number ahead of expected")
	ErrUnsupported    = errors.New("sctpaddr: unsupported chunk or parameter")
)
