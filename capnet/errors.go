package capnet

import "errors"

var (
	// ErrBadHandle is returned for zero, forged, foreign-host or
	// token-mismatching handles.
	ErrBadHandle = errors.New("capnet: invalid or unknown endpoint handle")

	// ErrStaleHandle is returned for handles bound to a trial generation that
	// has ended.
	ErrStaleHandle = errors.New("capnet: handle belongs to an ended trial generation")

	// ErrTrialEnded is returned when host-side minting is attempted on an
	// ended trial.
	ErrTrialEnded = errors.New("capnet: trial generation has ended")

	// ErrDeadlineExceeded is wrapped by the net.Error returned when a
	// simulated deadline is reached.
	ErrDeadlineExceeded = errors.New("capnet: simulated deadline exceeded")

	// ErrScriptExhausted is returned when a simulated endpoint has no more
	// scripted data to deliver and is not closed.
	ErrScriptExhausted = errors.New("capnet: simulated endpoint script exhausted")

	// ErrUnexpectedWrite is returned when a write does not match the next
	// Script.ExpectWrites entry.
	ErrUnexpectedWrite = errors.New("capnet: simulated endpoint received unexpected write")
)
