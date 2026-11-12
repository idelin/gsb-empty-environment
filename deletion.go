package netsec

import (
	"sort"
	"sync"
	"time"
)

// DeletionEvent records that one individual's data was erased. The generation
// is a monotonically increasing counter assigned by the deletion log; every
// training task pins the generation it observed at start time.
type DeletionEvent struct {
	IndividualID string
	Generation   int64
	OccurredAt   time.Time
}

// DeletionLog is the privacy host's append-only record of individual data
// deletions. It is safe for concurrent use.
type DeletionLog struct {
	mu     sync.RWMutex
	events []DeletionEvent
	next   int64
	clock  func() time.Time
}

// NewDeletionLog creates an empty deletion log. Generation numbering starts at
// one for the first recorded deletion.
func NewDeletionLog(opts ...Option) *DeletionLog {
	config := defaultConfig()
	for _, opt := range opts {
		opt(config)
	}
	return &DeletionLog{next: 1, clock: config.clock}
}

// Record appends a deletion for the individual and returns the assigned event.
// Recording an individual that was already erased is a no-op: the existing
// event is returned rather than minting a second generation for the same data.
func (l *DeletionLog) Record(individualID string) DeletionEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, event := range l.events {
		if event.IndividualID == individualID {
			return event
		}
	}
	event := DeletionEvent{
		IndividualID: individualID,
		Generation:   l.next,
		OccurredAt:   l.clock(),
	}
	l.next++
	l.events = append(l.events, event)
	return event
}

// CurrentGeneration returns the generation of the most recent deletion, or
// zero when no deletion has ever been recorded.
func (l *DeletionLog) CurrentGeneration() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.next - 1
}

// Events returns a copy of every deletion recorded so far, in generation order.
func (l *DeletionLog) Events() []DeletionEvent {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]DeletionEvent, len(l.events))
	copy(out, l.events)
	return out
}

// Since returns copies of every deletion whose generation is strictly greater
// than generation, i.e. every deletion a task pinned at generation has not
// incorporated.
func (l *DeletionLog) Since(generation int64) []DeletionEvent {
	l.mu.RLock()
	defer l.mu.RUnlock()
	idx := sort.Search(len(l.events), func(i int) bool {
		return l.events[i].Generation > generation
	})
	out := make([]DeletionEvent, len(l.events)-idx)
	copy(out, l.events[idx:])
	return out
}

// IsDeleted reports whether individualID has ever been erased.
func (l *DeletionLog) IsDeleted(individualID string) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, event := range l.events {
		if event.IndividualID == individualID {
			return true
		}
	}
	return false
}
