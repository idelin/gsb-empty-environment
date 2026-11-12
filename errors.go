package netsec

import (
	"errors"
	"fmt"
)

var (
	// ErrNoVerifier means the Reader was constructed without a signature
	// verifier.
	ErrNoVerifier = errors.New("netsec: no signature verifier configured")

	// ErrInvalidSignature means the injected verifier rejected a signed tree
	// head.
	ErrInvalidSignature = errors.New("netsec: tree head signature verification failed")

	// ErrNoVerifiedHead means a block was submitted before any tree head was
	// accepted.
	ErrNoVerifiedHead = errors.New("netsec: no verified tree head")

	// ErrFrozen means the reader detected same-length equivocation and refuses
	// all further updates.
	ErrFrozen = errors.New("netsec: log is frozen after equivocation")

	// ErrRollback means a new tree head in the current fork has a shorter
	// length than the already verified head.
	ErrRollback = errors.New("netsec: tree head length rolls back")

	// ErrForkRollback means a tree head names an earlier fork than the already
	// verified head.
	ErrForkRollback = errors.New("netsec: tree head fork rolls back")

	// ErrEquivocation means two validly signed tree heads for the same fork
	// and length have different roots.
	ErrEquivocation = errors.New("netsec: signed tree heads disagree at the same length")

	// ErrBlockRange means a block index is outside the verified tree.
	ErrBlockRange = errors.New("netsec: block index outside verified tree")

	// ErrInclusionProof means a block's inclusion proof does not fold up to
	// the verified root.
	ErrInclusionProof = errors.New("netsec: invalid inclusion proof")

	// ErrBlockConflict means a different block body was already stored at the
	// same index.
	ErrBlockConflict = errors.New("netsec: block conflicts with previously stored block")
)

// RollbackError describes a rejected length rollback.
type RollbackError struct {
	Fork           uint64
	KnownLength    uint64
	ReceivedLength uint64
}

func (e *RollbackError) Error() string {
	return fmt.Sprintf(
		"netsec: tree head for fork %d rolls back from length %d to %d",
		e.Fork, e.KnownLength, e.ReceivedLength,
	)
}

// Is makes errors.Is(err, ErrRollback) work for RollbackError.
func (e *RollbackError) Is(target error) bool {
	return target == ErrRollback
}

// ForkRollbackError describes a rejected fork rollback.
type ForkRollbackError struct {
	KnownFork    uint64
	ReceivedFork uint64
}

func (e *ForkRollbackError) Error() string {
	return fmt.Sprintf(
		"netsec: tree head fork rolls back from %d to %d",
		e.KnownFork, e.ReceivedFork,
	)
}

// Is makes errors.Is(err, ErrForkRollback) work for ForkRollbackError.
func (e *ForkRollbackError) Is(target error) bool {
	return target == ErrForkRollback
}

// EquivocationError carries both validly signed tree heads that disagree at
// the same fork and length. The heads are also retained by Reader.Evidence.
type EquivocationError struct {
	Existing SignedTreeHead
	Incoming SignedTreeHead
}

func (e *EquivocationError) Error() string {
	return fmt.Sprintf(
		"netsec: fork %d has two signed roots at length %d (%s and %s)",
		e.Existing.Head.Fork,
		e.Existing.Head.Length,
		e.Existing.Head.Root,
		e.Incoming.Head.Root,
	)
}

// Is makes errors.Is(err, ErrEquivocation) work for EquivocationError.
func (e *EquivocationError) Is(target error) bool {
	return target == ErrEquivocation
}
