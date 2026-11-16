package netsec

import "errors"

var (
	// ErrInvalidConfig reports an invalid round configuration.
	ErrInvalidConfig = errors.New("netsec: invalid round configuration")
	// ErrRoundExists reports that a round ID is already bound to a different
	// participant set or deadline.
	ErrRoundExists = errors.New("netsec: round already exists")
	// ErrRoundNotFound reports an unknown round ID.
	ErrRoundNotFound = errors.New("netsec: round not found")
	// ErrInvalidParticipant reports a participant outside the frozen set.
	ErrInvalidParticipant = errors.New("netsec: participant is not in the frozen set")
	// ErrTooManyParticipants reports more than MaxParticipants participants.
	ErrTooManyParticipants = errors.New("netsec: too many participants")
	// ErrDuplicateParticipant reports a repeated participant ID.
	ErrDuplicateParticipant = errors.New("netsec: duplicate participant")

	// ErrInvalidCommitment reports a commitment that is not CommitmentSize bytes.
	ErrInvalidCommitment = errors.New("netsec: invalid commitment length")
	// ErrInvalidNonce reports a nonce that is not NonceSize bytes.
	ErrInvalidNonce = errors.New("netsec: invalid nonce length")
	// ErrCommitmentConflict reports a second, different commitment.
	ErrCommitmentConflict = errors.New("netsec: participant already committed to a different value")
	// ErrNoCommitment reports a reveal for a participant with no commitment.
	ErrNoCommitment = errors.New("netsec: participant has no commitment")
	// ErrRevealMismatch reports a nonce that does not open the commitment.
	ErrRevealMismatch = errors.New("netsec: reveal does not match commitment")
	// ErrRevealConflict reports a second, different reveal.
	ErrRevealConflict = errors.New("netsec: participant already revealed a different value")
	// ErrEarlyReveal reports a reveal before every commitment is present.
	ErrEarlyReveal = errors.New("netsec: reveal before all commitments")

	// ErrRoundAborted reports an aborted round. Aborted rounds have no result.
	ErrRoundAborted = errors.New("netsec: round aborted")
	// ErrRoundFinalized reports an operation that is invalid after finalization.
	ErrRoundFinalized = errors.New("netsec: round already finalized")
	// ErrDeadlineExceeded reports that the round deadline has passed.
	ErrDeadlineExceeded = errors.New("netsec: round deadline exceeded")
	// ErrNotExpired reports an explicit timeout before the deadline.
	ErrNotExpired = errors.New("netsec: round deadline has not been reached")
	// ErrNotFinalized reports a result request before finalization.
	ErrNotFinalized = errors.New("netsec: round is not finalized")

	// ErrNilJournal reports a missing Journal.
	ErrNilJournal = errors.New("netsec: nil journal")
	// ErrJournalCorrupt reports a malformed journal record.
	ErrJournalCorrupt = errors.New("netsec: corrupt journal")
	// ErrJournalClosed reports use of a closed FileJournal.
	ErrJournalClosed = errors.New("netsec: journal closed")
)
