// Package receipt implements an event-sourced receipt (回执) log.
//
// An executor reports business progress as receipts carrying an execution
// generation (执行代号) and a monotonically increasing revision (单调修订).
// The store enforces:
//
//   - same revision with a contradictory status  -> conflict, rejected
//   - lower revision than the current one        -> archived only (evidence kept)
//   - higher revision                            -> advances state, but a terminal
//     generation never goes back to a non-terminal status
//
// A generation can be explicitly superseded by a resolution. The successor
// generation may advance business only after the resolution is committed;
// receipts arriving earlier are buffered. Late terminal receipts of a
// superseded generation never overwrite the successor, but are recorded as
// pending compensation effects (待补偿效果).
package receipt

import "fmt"

// Status is the business status reported by a receipt.
type Status string

const (
	StatusAccepted  Status = "accepted"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

// Terminal reports whether the status is terminal (succeeded / failed).
func (s Status) Terminal() bool { return s == StatusSucceeded || s == StatusFailed }

func (s Status) valid() bool {
	switch s {
	case StatusAccepted, StatusRunning, StatusSucceeded, StatusFailed:
		return true
	}
	return false
}

// Receipt is a single status report for an execution generation.
type Receipt struct {
	Generation string `json:"generation"`
	Revision   int64  `json:"revision"`
	Status     Status `json:"status"`
	Evidence   string `json:"evidence"`
}

func (r Receipt) validate() error {
	if r.Generation == "" {
		return fmt.Errorf("receipt: empty generation")
	}
	if r.Revision < 0 {
		return fmt.Errorf("receipt: negative revision %d", r.Revision)
	}
	if !r.Status.valid() {
		return fmt.Errorf("receipt: invalid status %q", r.Status)
	}
	return nil
}

// Compensation is a pending effect recorded when a superseded generation
// reports a late terminal receipt.
type Compensation struct {
	Generation string `json:"generation"`
	Revision   int64  `json:"revision"`
	Status     Status `json:"status"`
	Evidence   string `json:"evidence"`
}

// ConflictError reports a same-revision contradictory receipt.
type ConflictError struct {
	Generation string
	Revision   int64
	Existing   Status
	Incoming   Status
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("receipt: conflict on generation %q revision %d: %s vs %s",
		e.Generation, e.Revision, e.Existing, e.Incoming)
}

// RegressionError reports a higher-revision receipt trying to move a
// terminal generation back to a non-terminal status.
type RegressionError struct {
	Generation string
	Revision   int64
	From       Status
	To         Status
}

func (e *RegressionError) Error() string {
	return fmt.Sprintf("receipt: generation %q is terminal (%s), revision %d cannot regress to %s",
		e.Generation, e.From, e.Revision, e.To)
}

// UnknownGenerationError reports a receipt for a generation that is neither
// current nor announced by any committed supersede resolution.
type UnknownGenerationError struct{ Generation string }

func (e *UnknownGenerationError) Error() string {
	return fmt.Sprintf("receipt: unknown generation %q (no committed supersede resolution)", e.Generation)
}
