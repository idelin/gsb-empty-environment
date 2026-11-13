package netsec

import (
	"context"
	"sync"
)

// Event is one immutable entry in the relay's event log.
type Event struct {
	Seq     uint64
	Payload []byte
}

// LogBounds describes the retained sequence window.
type LogBounds struct {
	// Earliest is the smallest sequence still retained; zero when the log is
	// empty.
	Earliest uint64
	// Newest is the largest sequence ever appended; zero when the log is empty.
	Newest uint64
}

// liveSub is a subscriber waiting at the live edge. Already-retained events at
// or above its cursor are buffered in queued; subsequent appends land there
// too, so a handoff can never lose or duplicate an event.
type liveSub struct {
	cursor uint64
	queued []Event
	wake   chan struct{}
	closed bool
}

func (s *liveSub) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Log is a goroutine-safe, gap-free in-memory event log with a retention
// window. Sequences assigned by Append are strictly increasing starting at 1
// with no gaps. It is the backing store Replayer replays from.
type Log struct {
	mu     sync.Mutex
	events []Event
	subs   map[*liveSub]struct{}
	next   uint64
}

// NewLog creates an empty event log.
func NewLog() *Log {
	return &Log{
		subs: make(map[*liveSub]struct{}),
		next: 1,
	}
}

// Append stores an event with the next contiguous sequence and returns it.
// The payload is copied so later caller mutation cannot corrupt the log. Every
// live subscriber whose cursor is at or below the new sequence is notified.
func (l *Log) Append(payload []byte) Event {
	l.mu.Lock()
	ev := Event{Seq: l.next, Payload: append([]byte(nil), payload...)}
	l.next++
	l.events = append(l.events, ev)
	for sub := range l.subs {
		if !sub.closed && sub.cursor <= ev.Seq {
			sub.queued = append(sub.queued, ev)
			sub.signal()
		}
	}
	l.mu.Unlock()
	return ev
}

// Bounds returns the current retention window.
func (l *Log) Bounds() LogBounds {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.events) == 0 {
		return LogBounds{}
	}
	return LogBounds{Earliest: l.events[0].Seq, Newest: l.events[len(l.events)-1].Seq}
}

// Read returns retained events with sequences in [from, to], capped at max
// entries. It returns ErrWindowRolled when from is older than the earliest
// retained sequence. An empty slice with nil error means no retained event
// currently falls in the requested range.
func (l *Log) Read(from, to uint64, max int) ([]Event, error) {
	if max <= 0 {
		max = 64
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.events) == 0 {
		return nil, nil
	}
	first := l.events[0].Seq
	if from < first {
		return nil, ErrWindowRolled
	}
	start := int(from - first)
	if start >= len(l.events) {
		return nil, nil
	}
	end := len(l.events)
	if to < l.events[start].Seq {
		return nil, nil
	}
	if limit := start + max; limit < end {
		end = limit
	}
	out := make([]Event, 0, end-start)
	for _, ev := range l.events[start:end] {
		if ev.Seq > to {
			break
		}
		out = append(out, ev)
	}
	return out, nil
}

// TrimThrough drops retained events whose sequence is at most seq. Subscribers
// still needing a dropped sequence are failed with ErrWindowRolled so their
// replay cannot silently skip events.
func (l *Log) TrimThrough(seq uint64) {
	l.mu.Lock()
	if len(l.events) > 0 {
		first := l.events[0].Seq
		if seq >= first {
			cut := int(seq-first) + 1
			if cut > len(l.events) {
				cut = len(l.events)
			}
			l.events = append([]Event(nil), l.events[cut:]...)
		}
	}
	for sub := range l.subs {
		if sub.closed {
			continue
		}
		// Drop queued events that are no longer retained; if the subscriber
		// still needs the dropped head it must be failed.
		kept := make([]Event, 0, len(sub.queued))
		failed := false
		for _, ev := range sub.queued {
			if ev.Seq <= seq {
				if ev.Seq >= sub.cursor {
					failed = true
				}
				continue
			}
			kept = append(kept, ev)
		}
		sub.queued = kept
		if failed {
			sub.closed = true
			sub.signal()
		}
	}
	l.mu.Unlock()
}

// OpenCursor hands a historical replay off to the live edge. It must be called
// while the caller still holds a consistent view of the requested cursor:
// under the log lock it snapshots every retained event at or above cursor,
// then registers for future appends. Returned events plus subsequent
// LiveCursor.Next results form a strictly contiguous, duplicate-free stream
// starting at cursor.
func (l *Log) OpenCursor(cursor uint64) *LiveCursor {
	l.mu.Lock()
	defer l.mu.Unlock()
	sub := &liveSub{cursor: cursor, wake: make(chan struct{}, 1)}
	if len(l.events) > 0 && cursor >= l.events[0].Seq {
		start := int(cursor - l.events[0].Seq)
		if start < len(l.events) {
			sub.queued = append(sub.queued, l.events[start:]...)
		}
	}
	l.subs[sub] = struct{}{}
	return &LiveCursor{log: l, sub: sub}
}

// LiveCursor is a contiguous tail subscription created by Log.OpenCursor.
type LiveCursor struct {
	log *Log
	sub *liveSub
}

// Next returns the next event in sequence order, blocking until it is
// appended or ctx is done. It returns ErrWindowRolled if retention trimmed an
// event this cursor still needed.
func (c *LiveCursor) Next(ctx context.Context) (Event, error) {
	for {
		c.log.mu.Lock()
		if len(c.sub.queued) > 0 {
			ev := c.sub.queued[0]
			c.sub.queued = c.sub.queued[1:]
			c.sub.cursor = ev.Seq + 1
			c.log.mu.Unlock()
			return ev, nil
		}
		if c.sub.closed {
			c.log.mu.Unlock()
			return Event{}, ErrWindowRolled
		}
		c.log.mu.Unlock()

		select {
		case <-ctx.Done():
			return Event{}, ctx.Err()
		case <-c.sub.wake:
		}
	}
}

// Cancel unregisters the tail subscription. It is idempotent.
func (c *LiveCursor) Cancel() {
	c.log.mu.Lock()
	if !c.sub.closed {
		c.sub.closed = true
		delete(c.log.subs, c.sub)
	}
	c.log.mu.Unlock()
}
