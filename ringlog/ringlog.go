// Package ringlog implements a fixed-capacity circular log with
// cursor-based readers.
//
// The log retains only the most recent Capacity records. Readers follow
// the log with a Cursor (generation + slot sequence). A reader that falls
// behind by more than the capacity receives a *LaggedError describing the
// exact range of records that were overwritten and are permanently lost.
// Overwritten slots are never mistaken for old records: every slot carries
// its absolute sequence number and is validated on read.
//
// Appends publish a whole batch atomically: concurrent readers observe
// either all of the batch or none of it. Reads are cancelable via context;
// a canceled read returns the input cursor unchanged (nothing is consumed).
package ringlog

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// Cursor is a reader's position in the log: the next record to read.
//
// Gen counts how many times the ring has wrapped; Seq is the slot index
// within the current generation, in [0, Capacity). The absolute position
// of a cursor is Gen*Capacity + Seq.
type Cursor struct {
	Gen uint64
	Seq uint64
}

func (c Cursor) abs(capacity uint64) uint64 { return c.Gen*capacity + c.Seq }

func cursorOf(abs, capacity uint64) Cursor {
	return Cursor{Gen: abs / capacity, Seq: abs % capacity}
}

// String renders the cursor as "gen:seq".
func (c Cursor) String() string { return fmt.Sprintf("%d:%d", c.Gen, c.Seq) }

// ErrFutureCursor is returned when a cursor points past the published tail.
var ErrFutureCursor = errors.New("ringlog: cursor is ahead of the published tail")

// ErrInvalidCursor is returned when a cursor's slot index is out of range.
var ErrInvalidCursor = errors.New("ringlog: cursor slot index out of range")

// LaggedError reports that a reader fell behind by more than the log
// capacity and some records it has not seen were overwritten.
//
// The half-open range [From, To) is the exact set of positions that were
// lost: From is the reader's cursor, To is the oldest position still
// retained. The reader may resume at To.
type LaggedError struct {
	From Cursor
	To   Cursor
}

func (e *LaggedError) Error() string {
	return fmt.Sprintf("ringlog: reader lagged, records [%s, %s) were overwritten", e.From, e.To)
}

// IsLagged reports whether err is a *LaggedError and, if so, returns it.
func IsLagged(err error) (*LaggedError, bool) {
	var le *LaggedError
	if errors.As(err, &le) {
		return le, true
	}
	return nil, false
}

// entry is one published record. A fresh value is stored per record, so a
// reader holding a pointer never observes a slot being mutated in place.
type entry struct {
	seq  uint64 // absolute sequence number; detects overwrite
	data []byte
}

// Log is a fixed-capacity circular log. The zero value is not usable;
// construct with New.
type Log struct {
	cap   uint64
	mu    sync.Mutex // serializes appends
	slots []atomic.Pointer[entry]
	tail  atomic.Uint64 // absolute position one past the last published record
}

// New returns a Log retaining the most recent capacity records.
// It panics if capacity is zero.
func New(capacity uint64) *Log {
	if capacity == 0 {
		panic("ringlog: capacity must be > 0")
	}
	return &Log{cap: capacity, slots: make([]atomic.Pointer[entry], capacity)}
}

// Capacity returns the ring capacity.
func (l *Log) Capacity() uint64 { return l.cap }

// Oldest returns the cursor of the oldest retained record.
func (l *Log) Oldest() Cursor { return cursorOf(l.head(), l.cap) }

// Latest returns the cursor one past the newest published record.
func (l *Log) Latest() Cursor { return cursorOf(l.tail.Load(), l.cap) }

func (l *Log) head() uint64 {
	t := l.tail.Load()
	if t > l.cap {
		return t - l.cap
	}
	return 0
}

// Append publishes batch as one atomic unit: concurrent readers see either
// the whole batch or none of it. If the batch is larger than the capacity,
// only its tail is retained. The batch slices must not be mutated after
// Append returns. Append returns the cursor one past the last appended
// record.
func (l *Log) Append(batch [][]byte) Cursor {
	if len(batch) == 0 {
		return cursorOf(l.tail.Load(), l.cap)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	base := l.tail.Load()
	for i, rec := range batch {
		abs := base + uint64(i)
		l.slots[abs%l.cap].Store(&entry{seq: abs, data: rec})
	}
	// Publish only after every slot is visible.
	l.tail.Store(base + uint64(len(batch)))
	return cursorOf(base+uint64(len(batch)), l.cap)
}

// Read returns up to max records starting at cur, plus the cursor for the
// following record. It never blocks waiting for new records.
//
//   - If cur was overwritten (reader lagged beyond capacity), Read returns
//     a *LaggedError with the exact lost range; the cursor is not consumed.
//   - If ctx is canceled, Read returns ctx.Err() and the input cursor
//     unchanged; the cursor is not consumed.
//   - If cur is ahead of the published tail, Read returns ErrFutureCursor.
func (l *Log) Read(ctx context.Context, cur Cursor, max int) ([][]byte, Cursor, error) {
	if err := ctx.Err(); err != nil {
		return nil, cur, err
	}
	if cur.Seq >= l.cap {
		return nil, cur, ErrInvalidCursor
	}
	if max <= 0 {
		return nil, cur, nil
	}
	abs := cur.abs(l.cap)
	tail := l.tail.Load()
	head := uint64(0)
	if tail > l.cap {
		head = tail - l.cap
	}
	if abs < head {
		return nil, cur, &LaggedError{From: cur, To: cursorOf(head, l.cap)}
	}
	if abs > tail {
		return nil, cur, ErrFutureCursor
	}
	limit := abs + uint64(max)
	if limit > tail {
		limit = tail
	}
	out := make([][]byte, 0, limit-abs)
	for a := abs; a < limit; a++ {
		if err := ctx.Err(); err != nil {
			return nil, cur, err
		}
		e := l.slots[a%l.cap].Load()
		if e == nil || e.seq != a {
			// The slot was overwritten by a concurrent append after we
			// sampled tail. Never serve it as an old record; report the
			// lost range against the current head instead.
			h := l.head()
			return nil, cur, &LaggedError{From: cur, To: cursorOf(h, l.cap)}
		}
		out = append(out, e.data)
	}
	return out, cursorOf(limit, l.cap), nil
}
