package netsec

import "errors"

var (
	// ErrSequenceRetired indicates a frame for an old, retired key sequence.
	ErrSequenceRetired = errors.New("netsec: key sequence is retired")

	// ErrSequenceSkipped indicates an attempt to activate a sequence other
	// than the current sequence plus one.
	ErrSequenceSkipped = errors.New("netsec: key sequence skipped")

	// ErrProtectionPeriod indicates a switch attempted before the caller's
	// protection period had elapsed.
	ErrProtectionPeriod = errors.New("netsec: key switch during protection period")

	// ErrFrameCounter indicates a duplicate or replayed frame counter for a
	// sequence and neighbor.
	ErrFrameCounter = errors.New("netsec: frame counter is not strictly increasing")
)
