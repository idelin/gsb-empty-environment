package main

import "fmt"

// StaleFenceError reports a write whose fence is not strictly greater
// than the highest fence the resource has already accepted.
type StaleFenceError struct {
	Fence uint64
	Last  uint64
}

func (e *StaleFenceError) Error() string {
	return fmt.Sprintf("stale fence rejected by resource (fence=%d, last=%d)", e.Fence, e.Last)
}

// WriteRecord is one accepted write in the resource's history.
type WriteRecord struct {
	Fence uint64
	Value string
}

// Resource is the shared resource. It accepts a write only if its
// fence is monotonically increasing (never lower than any fence it
// has accepted before), so a late write from an expired master can
// never overwrite newer data. An equal fence can only come from the
// same master retrying its own write, which is safe to accept.
type Resource struct {
	lastFence uint64
	data      string
	history   []WriteRecord
}

func NewResource() *Resource { return &Resource{} }

// Write applies value iff fence is monotonically increasing.
func (r *Resource) Write(fence uint64, value string) error {
	if fence < r.lastFence {
		return &StaleFenceError{Fence: fence, Last: r.lastFence}
	}
	r.lastFence = fence
	r.data = value
	r.history = append(r.history, WriteRecord{Fence: fence, Value: value})
	return nil
}

func (r *Resource) Data() string { return r.data }

// History returns the accepted write history, in acceptance order.
func (r *Resource) History() []WriteRecord {
	out := make([]WriteRecord, len(r.history))
	copy(out, r.history)
	return out
}
