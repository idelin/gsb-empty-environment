// Package agglog implements an aggregate log: runs of same-key
// increments are compressed into batches that list the original event
// ids and a summary of each increment. Batches can be replayed and can
// overlap events that were already applied; only unseen events take
// effect, and an overlapping id carrying a different increment is
// reported as a conflict.
package agglog

import "fmt"

// Event is a single increment addressed to a key.
type Event struct {
	ID    string
	Key   string
	Delta int64
}

// Entry summarizes one increment inside a Batch: the original event id
// and the delta it carried.
type Entry struct {
	EventID string
	Delta   int64
}

// Batch is a compressed run of same-key increments. Entries keep the
// original event order.
type Batch struct {
	Key     string
	Entries []Entry
}

// Pack compresses events into a Batch. All events must share one key;
// their order is preserved in the batch.
func Pack(events []Event) (Batch, error) {
	if len(events) == 0 {
		return Batch{}, fmt.Errorf("agglog: cannot pack an empty event list")
	}
	key := events[0].Key
	b := Batch{Key: key, Entries: make([]Entry, len(events))}
	for i, e := range events {
		if e.Key != key {
			return Batch{}, fmt.Errorf("agglog: cannot pack mixed keys %q and %q", key, e.Key)
		}
		b.Entries[i] = Entry{EventID: e.ID, Delta: e.Delta}
	}
	return b, nil
}

// Unpack expands a batch back into its events, in listed order.
func Unpack(b Batch) []Event {
	events := make([]Event, len(b.Entries))
	for i, en := range b.Entries {
		events[i] = Event{ID: en.EventID, Key: b.Key, Delta: en.Delta}
	}
	return events
}

// ConflictError reports an event id that was already applied (or listed
// earlier in the same batch) with a different delta.
type ConflictError struct {
	EventID  string
	Existing int64
	Incoming int64
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("agglog: event %q already applied with delta %d, conflicting delta %d",
		e.EventID, e.Existing, e.Incoming)
}

// Log is the aggregate log. Alongside per-key totals it keeps the set
// of applied event ids (the per-event set model), which makes batch
// application idempotent under replay and overlap.
type Log struct {
	totals  map[string]int64
	applied map[string]int64 // event id -> applied delta
}

// New returns an empty Log.
func New() *Log {
	return &Log{
		totals:  make(map[string]int64),
		applied: make(map[string]int64),
	}
}

// Total returns the aggregated value for key.
func (l *Log) Total(key string) int64 {
	return l.totals[key]
}

// Applied reports whether id has been applied, and with which delta.
func (l *Log) Applied(id string) (delta int64, ok bool) {
	delta, ok = l.applied[id]
	return delta, ok
}

// ApplyEvent applies a single event. Re-applying the same id with the
// same delta is a no-op; a different delta is a conflict.
func (l *Log) ApplyEvent(e Event) error {
	if prev, ok := l.applied[e.ID]; ok {
		if prev != e.Delta {
			return &ConflictError{EventID: e.ID, Existing: prev, Incoming: e.Delta}
		}
		return nil
	}
	l.applied[e.ID] = e.Delta
	l.totals[e.Key] += e.Delta
	return nil
}

// ApplyBatch applies the unseen entries of b in listed order. Entries
// whose id was already applied with the same delta are skipped. If any
// entry conflicts with a previously applied delta (or with another
// entry of the same id inside b), the whole batch is rejected with a
// ConflictError and nothing is applied.
func (l *Log) ApplyBatch(b Batch) error {
	seen := make(map[string]int64, len(b.Entries))
	for _, en := range b.Entries {
		if prev, ok := l.applied[en.EventID]; ok && prev != en.Delta {
			return &ConflictError{EventID: en.EventID, Existing: prev, Incoming: en.Delta}
		}
		if prev, ok := seen[en.EventID]; ok && prev != en.Delta {
			return &ConflictError{EventID: en.EventID, Existing: prev, Incoming: en.Delta}
		}
		seen[en.EventID] = en.Delta
	}
	for _, en := range b.Entries {
		if _, ok := l.applied[en.EventID]; ok {
			continue
		}
		l.applied[en.EventID] = en.Delta
		l.totals[b.Key] += en.Delta
	}
	return nil
}
