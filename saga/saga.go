// Package saga implements a long-running transaction engine with
// compensation (Saga pattern) and a journaled, crash-recoverable
// state machine. Only the Go standard library is used.
//
// A business transaction is described as a Definition: an ordered list
// of Steps. Each Step has an Action (an external call) and an optional
// Compensate (the reverse operation). Every state transition is
// appended to a per-transaction journal on disk before the engine
// acts on it, so a power loss never loses or duplicates effects:
// completed steps are skipped on recovery, and interrupted external
// calls are retried with the same idempotency key.
package saga

import (
	"errors"
	"time"
)

// ErrAwait is returned by a Step Action to indicate the step submitted
// its request to an external system and is now waiting for an
// asynchronous callback (see Engine.Deliver) or a timeout.
var ErrAwait = errors.New("saga: awaiting external callback")

// ErrTimeout is the failure reason recorded when a step's deadline
// elapses before the external callback arrives.
var ErrTimeout = errors.New("saga: step timed out waiting for external callback")

// Status is the lifecycle state of a transaction.
type Status string

const (
	StatusRunning      Status = "RUNNING"      // executing forward steps
	StatusWaiting      Status = "WAITING"      // parked, awaiting external callback
	StatusCompensating Status = "COMPENSATING" // running reverse compensation
	StatusCompleted    Status = "COMPLETED"    // all steps succeeded (terminal)
	StatusCompensated  Status = "COMPENSATED"  // fully rolled back (terminal)
	StatusFailed       Status = "FAILED"       // compensation itself failed (terminal)
)

// Terminal reports whether the status is a final state.
func (s Status) Terminal() bool {
	return s == StatusCompleted || s == StatusCompensated || s == StatusFailed
}

// EventType identifies a journaled state transition.
type EventType string

const (
	EvTxStarted           EventType = "TxStarted"
	EvStepStarted         EventType = "StepStarted"
	EvStepCompleted       EventType = "StepCompleted"
	EvAwaitingCallback    EventType = "AwaitingCallback"
	EvCallbackReceived    EventType = "CallbackReceived"
	EvStepFailed          EventType = "StepFailed"
	EvStepTimedOut        EventType = "StepTimedOut"
	EvCompensateStarted   EventType = "CompensateStarted"
	EvCompensateCompleted EventType = "CompensateCompleted"
	EvTxCompleted         EventType = "TxCompleted"
	EvTxCompensated       EventType = "TxCompensated"
	EvTxFailed            EventType = "TxFailed"
)

// Event is one entry of the persistent audit trail. The journal of a
// transaction is an ordered sequence of events; replaying it rebuilds
// the exact in-memory state machine.
type Event struct {
	Seq      int64                  `json:"seq"`
	TxID     string                 `json:"tx"`
	Time     time.Time              `json:"time"`
	Type     EventType              `json:"type"`
	Def      string                 `json:"def,omitempty"`
	Idx      int                    `json:"idx,omitempty"`
	Step     string                 `json:"step,omitempty"`
	Key      string                 `json:"key,omitempty"`
	Output   interface{}            `json:"output,omitempty"`
	Err      string                 `json:"err,omitempty"`
	Deadline time.Time              `json:"deadline,omitempty"`
	Data     map[string]interface{} `json:"data,omitempty"`
}

// StepContext is handed to Action and Compensate functions.
type StepContext struct {
	TxID string
	Step string
	// IdempotencyKey is stable across crashes and retries of the same
	// step. External systems must deduplicate on it so retried calls
	// after a power loss are applied at most once.
	IdempotencyKey string
	// Data is the shared, journaled bag carried across steps. Actions
	// store values here (e.g. an order id) for later steps and for
	// compensation.
	Data map[string]interface{}
}

// Step is one state transition plus its external call and its reverse
// compensation.
type Step struct {
	Name string
	// Timeout bounds how long the engine waits for an external
	// callback after the Action returned ErrAwait. Zero means wait
	// forever. For long transactions this is typically hours.
	Timeout time.Duration
	// Action performs the forward external call. Return ErrAwait to
	// park the transaction until Engine.Deliver or the timeout.
	Action func(ctx *StepContext) (interface{}, error)
	// Compensate undoes the Action. It receives the same idempotency
	// key and the journaled Data. May be nil (nothing to undo).
	Compensate func(ctx *StepContext) error
}

// Definition is the static shape of a long-running transaction.
type Definition struct {
	Name  string
	Steps []Step
}
