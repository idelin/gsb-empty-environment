package main

import "fmt"

// StaleFenceError is returned when a write carries a fence that is not
// strictly greater than the highest fence the resource has accepted.
type StaleFenceError struct {
	Fence, Last uint64
	Writer      string
}

func (e *StaleFenceError) Error() string {
	return fmt.Sprintf("stale fence: %d < last accepted %d (writer %s)",
		e.Fence, e.Last, e.Writer)
}

// WriteRecord is one accepted write, in acceptance order.
type WriteRecord struct {
	Seq    int
	Fence  uint64
	Writer string
	Value  string
	At     int64
}

// Resource is the shared resource. It rejects any write whose fence is
// lower than the highest fence it has ever accepted — including fences
// seen before a crash. Fences are unique per lease grant, so an equal
// fence can only come from the same epoch (an idempotent retry); a
// lower fence can only come from a stale leader.
type Resource struct {
	clock     *Clock
	store     *Store
	data      string
	lastFence uint64
	history   []WriteRecord
}

func NewResource(clock *Clock, store *Store) *Resource {
	return &Resource{clock: clock, store: store}
}

// Recover reloads the durable fence and data. The fence may only move
// forward: recovery can never lower it.
func (r *Resource) Recover() error {
	st, err := r.store.Load()
	if err != nil {
		return err
	}
	if st.LastFence > r.lastFence {
		r.lastFence = st.LastFence
		r.data = st.Data
		r.history = st.History
	}
	return nil
}

// Write applies the lease's fence. An expired old leader's late write
// carries a lower fence and is rejected here, at the resource — the
// resource does not trust the holder, only the fence order.
func (r *Resource) Write(l *Lease, value string) error {
	if l.Fence < r.lastFence {
		return &StaleFenceError{Fence: l.Fence, Last: r.lastFence, Writer: l.Holder}
	}
	r.lastFence = l.Fence
	r.data = value
	r.history = append(r.history, WriteRecord{
		Seq: len(r.history) + 1, Fence: l.Fence,
		Writer: l.Holder, Value: value, At: r.clock.Now(),
	})
	if err := r.store.Save(persistentState{
		LastFence: r.lastFence, Data: r.data, History: r.history,
	}); err != nil {
		return err
	}
	return nil
}

func (r *Resource) Data() string           { return r.data }
func (r *Resource) LastFence() uint64      { return r.lastFence }
func (r *Resource) History() []WriteRecord { return r.history }
