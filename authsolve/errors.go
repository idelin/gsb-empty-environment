package authsolve

import (
	"errors"
	"fmt"

	"prescreen.local/ps-e39099b0dc/clockwire"
)

// The solver keeps these failure classes distinct so a release host can make
// an appropriate decision (retry clock acquisition, reject the credential
// bundle, or reject the selected witness) instead of collapsing everything
// into a generic error.
var (
	// ErrInvalidInput means the problem description itself is malformed.
	ErrInvalidInput = errors.New("authsolve: invalid input")

	// ErrClockInconsistent means the clock evidence has no solution.
	ErrClockInconsistent = errors.New("authsolve: inconsistent clock evidence")

	// ErrWitnessNotRobust means a proposed witness fails for at least one
	// clock-offset assignment allowed by the evidence.
	ErrWitnessNotRobust = errors.New("authsolve: witness is not robust")

	// ErrNoWitness means no fixed witness simultaneously satisfies the policy
	// and is robust over all allowed clock offsets.
	ErrNoWitness = errors.New("authsolve: no fixed robust witness")

	// ErrSignature means an Ed25519 credential signature is malformed or does
	// not verify.
	ErrSignature = errors.New("authsolve: invalid credential signature")

	// ErrSignedOverflow means interval arithmetic would leave the signed
	// 64-bit domain.  It is separate from an infeasible interval.
	ErrSignedOverflow = fmt.Errorf("authsolve: signed 64-bit overflow: %w", clockwire.ErrOverflow)
)

// Compatibility spellings used by different release hosts.
var (
	ErrClockContradiction = ErrClockInconsistent
	ErrInconsistentClock  = ErrClockInconsistent
	ErrNotRobust          = ErrWitnessNotRobust
	ErrNonRobustWitness   = ErrWitnessNotRobust
	ErrNoRobustWitness    = ErrNoWitness
	ErrBadSignature       = ErrSignature
	ErrOverflow           = ErrSignedOverflow
)
