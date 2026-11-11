// Package netsec implements an audit-first commit pipeline for security
// hooks. A sensitive operation is never executed until its audit event has
// been durably reserved; the side effect runs only after the reservation is
// acknowledged, and its outcome is recorded under the same operation ID.
package netsec

import (
	"context"
	"errors"
)

// Status is the lifecycle state of an audited operation.
type Status uint8

const (
	// StatusUnknown is never persisted; it is the zero value used when an
	// operation is not present in the log.
	StatusUnknown Status = 0

	// StatusPending means a durable audit slot was reserved but no terminal
	// result has been recorded yet. The side effect may be in flight, or the
	// process may have crashed before recording the outcome.
	StatusPending Status = 1

	// StatusSucceeded means the side effect completed without error.
	StatusSucceeded Status = 2

	// StatusFailed means the side effect ran and returned an error, which is
	// stored in the same terminal record.
	StatusFailed Status = 3
)

func (s Status) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusSucceeded:
		return "succeeded"
	case StatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Operation is the audit identity and payload submitted by the hook host.
type Operation struct {
	// OpID is the caller-assigned idempotency key. Two commits with the same
	// OpID describe the same logical operation and must never trigger the side
	// effect twice.
	OpID string

	// Actor is the principal that requested the operation.
	Actor string

	// Action is the verb describing the sensitive operation.
	Action string

	// Detail is an optional, human-readable payload.
	Detail string
}

// Record is one durable audit record. Pending records reserve a slot;
// succeeded/failed records are the terminal outcome of the same OpID.
type Record struct {
	// Seq is the monotonically increasing, gap-free position in the log.
	Seq uint64

	OpID   string
	Actor  string
	Action string
	Detail string

	// Status is the lifecycle state represented by this record.
	Status Status

	// Failure holds the side-effect error message when Status is
	// StatusFailed; it is empty for every other status.
	Failure string
}

// Result is the terminal view of an operation as seen by the hook host.
type Result struct {
	OpID   string
	Seq    uint64
	Status Status

	// Failure is non-nil only when Status is StatusFailed.
	Failure error

	// Replayed is true when the result was taken from an existing record
	// rather than produced by a fresh side-effect invocation.
	Replayed bool
}

// SideEffect performs the sensitive action. It is invoked at most once per
// OpID across the lifetime of the audit log. Returning an error does not lose
// the audit trail: a failed terminal record is appended under the same OpID.
type SideEffect func(ctx context.Context) error

// Sink is the durable, bounded audit log used by a Committer. Implementations
// must make Append durable before returning a nil error; a nil error is the
// durability acknowledgement that gates the side effect.
type Sink interface {
	// Append durably stores rec. It returns ErrSinkFull when the bounded
	// storage cannot accept the record, or a wrapped fault error otherwise.
	Append(ctx context.Context, rec Record) error

	// Replay returns every durable record in append order. It is used once at
	// startup to recover pending operations.
	Replay(ctx context.Context) ([]Record, error)

	// Compact reclaims space while keeping the newest record of every OpID.
	// Pending records must never be removed.
	Compact(ctx context.Context) error

	// Close releases any open file handle.
	Close() error
}

// Faults injects failures into a sink for verification. Every hook is
// optional: a nil hook means "do not inject". Returning a non-nil error fails
// the corresponding phase exactly once for that call.
type Faults struct {
	// BeforeAppend runs before the record is made durable. An error here
	// models a power loss before the write is acknowledged: no record exists.
	BeforeAppend func(rec Record) error

	// AfterAppend runs after durability is acknowledged. An error here models
	// a crash after the log is stable but before the process can continue; the
	// record must survive reopen.
	AfterAppend func(rec Record) error
}

var (
	// ErrSinkFull means the bounded audit storage has no room. This is the
	// backpressure signal: callers must retry later, and no side effect ran.
	ErrSinkFull = errors.New("audit sink full")

	// ErrClosed means the sink or committer has been closed.
	ErrClosed = errors.New("audit sink closed")

	// ErrEmptyOpID means an operation without an idempotency key was submitted.
	ErrEmptyOpID = errors.New("operation id is empty")

	// ErrPending means the OpID already holds a durable pending slot whose
	// outcome is unknown (for example after a crash). The operation is not
	// re-executed automatically; resolve it explicitly.
	ErrPending = errors.New("audit operation still pending")

	// ErrCorrupt means the durable log cannot be decoded safely.
	ErrCorrupt = errors.New("audit log corrupt")
)
