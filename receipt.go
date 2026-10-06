// Package receipt implements an event-sourced receipt log for execution
// epochs. Receipts carry a monotonic revision inside an execution epoch;
// epochs are replaced only by explicit supersede decisions.
package receipt

import "errors"

type Status string

const (
	StatusAccepted  Status = "accepted"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

func (s Status) valid() bool {
	switch s {
	case StatusAccepted, StatusRunning, StatusSucceeded, StatusFailed:
		return true
	}
	return false
}

// Terminal reports whether the status is a final state (succeeded/failed).
func (s Status) Terminal() bool {
	return s == StatusSucceeded || s == StatusFailed
}

// Receipt is a single status report for one execution epoch of an item.
type Receipt struct {
	ItemID   string
	Epoch    uint64
	Revision uint64
	Status   Status
	Payload  string
}

// SupersedeDecision explicitly replaces epoch From with epoch To. Only after
// the decision is committed may the new epoch advance the business head.
type SupersedeDecision struct {
	ItemID string
	From   uint64
	To     uint64
	Reason string
}

// Compensation marks a side effect of a superseded epoch that still needs
// to be compensated (the old epoch reached succeeded after being replaced).
type Compensation struct {
	ItemID string
	Epoch  uint64
	Status Status
}

var (
	ErrConflict      = errors.New("receipt: same epoch/revision carries different content")
	ErrUnknownItem   = errors.New("receipt: unknown item")
	ErrItemExists    = errors.New("receipt: item already registered")
	ErrInvalidStatus = errors.New("receipt: invalid status")
	ErrStaleDecision = errors.New("receipt: supersede decision does not match the current epoch")
	ErrNotPersistent = errors.New("receipt: store has no persistence configured")
)
