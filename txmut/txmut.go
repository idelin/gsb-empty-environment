// Package txmut executes mutation candidates against a test service
// transactionally. Every trial starts from a fixed snapshot taken once
// up front; an instance becomes reusable only after the complete trial
// receipt has been persisted and the instance has been restored to that
// snapshot. Any failure along the way quarantines the instance so that
// partially mutated server state can never leak into later samples.
package txmut

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Phase identifies the stage of a trial in which a failure occurred.
type Phase string

const (
	PhaseSnapshot Phase = "snapshot"
	PhaseReceive  Phase = "receive"
	PhaseReceipt  Phase = "receipt"
	PhaseRestore  Phase = "restore"
)

// Error wraps a failure with the trial phase that produced it.
type Error struct {
	Phase Phase
	Err   error
}

func (e *Error) Error() string { return fmt.Sprintf("txmut: %s phase failed: %v", e.Phase, e.Err) }
func (e *Error) Unwrap() error { return e.Err }

// ErrQuarantined is returned by RunTrial when the instance has been
// quarantined after an earlier failure and must not be reused.
var ErrQuarantined = errors.New("txmut: instance quarantined")

// ErrBusy is returned by RunTrial when a trial is already in flight.
var ErrBusy = errors.New("txmut: instance busy")

// Service is implemented by the test service under mutation. Snapshot
// captures the current server state, Restore rolls the server back to a
// previously captured snapshot, and Deliver feeds one (possibly mutated)
// candidate message to the server.
type Service interface {
	Snapshot(ctx context.Context) (snapshotID string, err error)
	Restore(ctx context.Context, snapshotID string) error
	Deliver(ctx context.Context, candidate []byte) (reply []byte, err error)
}

// Receipt is the durable record of one completed trial.
type Receipt struct {
	TrialID    string
	SnapshotID string
	Candidate  []byte
	Reply      []byte
	FinishedAt time.Time
}

// ReceiptStore persists trial receipts. WriteReceipt must return only
// after the receipt is durably recorded.
type ReceiptStore interface {
	WriteReceipt(ctx context.Context, r Receipt) error
}

// State describes the lifecycle of an Executor instance.
type State int

const (
	// StateReady means the instance sits at the fixed snapshot and may
	// run the next trial.
	StateReady State = iota
	// StateBusy means a trial is currently in flight.
	StateBusy
	// StateQuarantined means a failure left the server state untrusted;
	// the instance must be discarded instead of reused.
	StateQuarantined
)

func (s State) String() string {
	switch s {
	case StateReady:
		return "ready"
	case StateBusy:
		return "busy"
	case StateQuarantined:
		return "quarantined"
	default:
		return "unknown"
	}
}

// Executor runs mutation trials against one Service instance. It is safe
// for concurrent use, but trials are strictly serialized per instance.
type Executor struct {
	mu     sync.Mutex
	svc    Service
	store  ReceiptStore
	snapID string
	state  State
}

// NewExecutor captures the fixed snapshot every later trial will start
// from. If the snapshot cannot be taken, no instance is created.
func NewExecutor(ctx context.Context, svc Service, store ReceiptStore) (*Executor, error) {
	snapID, err := svc.Snapshot(ctx)
	if err != nil {
		return nil, &Error{Phase: PhaseSnapshot, Err: err}
	}
	return &Executor{svc: svc, store: store, snapID: snapID, state: StateReady}, nil
}

// State reports the current lifecycle state of the instance.
func (e *Executor) State() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state
}

// SnapshotID returns the fixed snapshot every trial starts from.
func (e *Executor) SnapshotID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.snapID
}

// RunTrial restores the fixed snapshot, delivers one mutated candidate,
// persists the complete trial receipt, and restores the snapshot again so
// the instance can be reused. The instance transitions to quarantined on
// any failure: an incomplete receipt means the trial outcome is unknown,
// and a failed restore means the server state can no longer be trusted.
func (e *Executor) RunTrial(ctx context.Context, trialID string, candidate []byte) (Receipt, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	switch e.state {
	case StateQuarantined:
		return Receipt{}, ErrQuarantined
	case StateBusy:
		return Receipt{}, ErrBusy
	}
	e.state = StateBusy

	quarantine := func(phase Phase, err error) (Receipt, error) {
		e.state = StateQuarantined
		return Receipt{}, &Error{Phase: phase, Err: err}
	}

	// Guarantee the candidate starts from the fixed snapshot even if a
	// previous trial left residue behind.
	if err := e.svc.Restore(ctx, e.snapID); err != nil {
		return quarantine(PhaseRestore, err)
	}

	reply, err := e.svc.Deliver(ctx, candidate)
	if err != nil {
		return quarantine(PhaseReceive, err)
	}

	receipt := Receipt{
		TrialID:    trialID,
		SnapshotID: e.snapID,
		Candidate:  append([]byte(nil), candidate...),
		Reply:      reply,
		FinishedAt: time.Now(),
	}
	if err := e.store.WriteReceipt(ctx, receipt); err != nil {
		return quarantine(PhaseReceipt, err)
	}

	// Only after the complete receipt is durable do we roll the server
	// back; a restore failure here quarantines the now-dirty instance.
	if err := e.svc.Restore(ctx, e.snapID); err != nil {
		return quarantine(PhaseRestore, err)
	}

	e.state = StateReady
	return receipt, nil
}
