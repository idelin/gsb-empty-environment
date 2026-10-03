package twopc

import "errors"

// ErrUnreachable is returned by a participant that is crashed or unreachable.
var ErrUnreachable = errors.New("participant unreachable")

// ErrUnknownTxn is returned when a participant is asked about a transaction
// it has never seen.
var ErrUnknownTxn = errors.New("unknown transaction")

// Vote is a participant's answer to a Prepare request.
type Vote string

const (
	VoteYes Vote = "YES"
	VoteNo  Vote = "NO"
)

// TxnState is the coordinator-side state of a transaction. Every transition
// is written to the WAL before the corresponding messages are sent, so the
// state machine can be replayed after a crash.
type TxnState string

const (
	StatePreparing  TxnState = "PREPARING"
	StatePrepared   TxnState = "PREPARED"
	StateCommitting TxnState = "COMMITTING"
	StateCommitted  TxnState = "COMMITTED"
	StateAborting   TxnState = "ABORTING"
	StateAborted    TxnState = "ABORTED"
)

// ParticipantState is the participant-side durable state of a transaction,
// returned by Query during recovery.
type ParticipantState string

const (
	PStateNone      ParticipantState = "NONE"
	PStatePrepared  ParticipantState = "PREPARED"
	PStateCommitted ParticipantState = "COMMITTED"
	PStateAborted   ParticipantState = "ABORTED"
)
