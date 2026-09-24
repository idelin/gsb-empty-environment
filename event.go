// Package cacheaudit implements a versioned cache with a bounded audit log,
// plus an auditor that checks recorded histories against a public
// consistency contract (linearizable, versioned reads).
package cacheaudit

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// Kind identifies the type of an audit event.
type Kind uint8

const (
	// KindCall is a Get call interval: Start/End bound the call, Version is
	// the returned source version, Outcome is hit/miss/error.
	KindCall Kind = iota + 1
	// KindLoad is a data-source load interval for a key.
	KindLoad
	// KindPublish is the point in time a version becomes visible.
	KindPublish
	// KindEvict is the point in time an entry is removed.
	KindEvict
)

func (k Kind) String() string {
	switch k {
	case KindCall:
		return "call"
	case KindLoad:
		return "load"
	case KindPublish:
		return "publish"
	case KindEvict:
		return "evict"
	}
	return "unknown"
}

// Outcome is the result classification of a KindCall event.
type Outcome uint8

const (
	// OutcomeHit means the call was served from a published cache entry.
	OutcomeHit Outcome = iota + 1
	// OutcomeMiss means the call was NOT served from a published entry
	// (it loaded, or waited on an unpublished in-flight load).
	OutcomeMiss
	// OutcomeError means the call failed.
	OutcomeError
)

func (o Outcome) String() string {
	switch o {
	case OutcomeHit:
		return "hit"
	case OutcomeMiss:
		return "miss"
	case OutcomeError:
		return "error"
	}
	return "unknown"
}

// Event is a single recorded audit record. Interval events (call, load) use
// Start/End; point events (publish, evict) use At.
type Event struct {
	Seq     uint64
	Kind    Kind
	Key     string
	Start   time.Time
	End     time.Time
	At      time.Time
	Version uint64
	Outcome Outcome
	Note    string
}

func (e Event) String() string {
	switch e.Kind {
	case KindCall:
		return fmt.Sprintf("#%d call %s [%s,%s] -> v%d (%s) %s",
			e.Seq, e.Key, fmtTime(e.Start), fmtTime(e.End), e.Version, e.Outcome, e.Note)
	case KindLoad:
		return fmt.Sprintf("#%d load %s [%s,%s] v%d %s",
			e.Seq, e.Key, fmtTime(e.Start), fmtTime(e.End), e.Version, e.Note)
	case KindPublish:
		return fmt.Sprintf("#%d publish %s v%d @%s", e.Seq, e.Key, e.Version, fmtTime(e.At))
	case KindEvict:
		return fmt.Sprintf("#%d evict %s @%s", e.Seq, e.Key, fmtTime(e.At))
	}
	return fmt.Sprintf("#%d ?", e.Seq)
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("15:04:05.000000")
}

// LogMeta describes the completeness of a log snapshot.
type LogMeta struct {
	Capacity int    // ring capacity of the source log
	Dropped  uint64 // events overwritten in the ring or reported lost
	Count    int    // events in the snapshot
	FirstSeq uint64 // smallest sequence number present
	LastSeq  uint64 // largest sequence number present
	// SeqVerified asks the auditor to treat sequence-number holes as
	// evidence of lost fragments. Snapshots taken from a Log set this;
	// hand-assembled or minimized histories leave it false.
	SeqVerified bool
}

// Complete reports whether the snapshot is known to be gap-free.
func (m LogMeta) Complete() bool {
	if m.Dropped > 0 {
		return false
	}
	if m.SeqVerified && m.Count > 0 && uint64(m.Count) != m.LastSeq-m.FirstSeq+1 {
		return false
	}
	return true
}

// Log is a bounded ring of audit events. Every event gets a global,
// monotonically increasing sequence number. Overwrites and explicitly lost
// records are counted so readers can detect incomplete evidence instead of
// guessing at the full history.
type Log struct {
	mu      sync.Mutex
	buf     []Event
	head    int
	size    int
	nextSeq uint64
	dropped uint64
}

// NewLog returns a bounded log holding at most capacity events.
func NewLog(capacity int) *Log {
	if capacity < 1 {
		capacity = 1
	}
	return &Log{buf: make([]Event, capacity), nextSeq: 1}
}

// Append assigns the next sequence number and stores the event, overwriting
// the oldest event when the ring is full.
func (l *Log) Append(e Event) Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	e.Seq = l.nextSeq
	l.nextSeq++
	if l.size < len(l.buf) {
		l.buf[(l.head+l.size)%len(l.buf)] = e
		l.size++
	} else {
		l.buf[l.head] = e
		l.head = (l.head + 1) % len(l.buf)
		l.dropped++
	}
	return e
}

// NoteLost records that n events could not be appended (e.g. the audit sink
// failed). The gap is surfaced as incomplete evidence to auditors.
func (l *Log) NoteLost(n uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dropped += n
}

// Snapshot returns the retained events ordered by sequence number plus
// metadata describing the completeness of the evidence.
func (l *Log) Snapshot() ([]Event, LogMeta) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Event, 0, l.size)
	for i := 0; i < l.size; i++ {
		out = append(out, l.buf[(l.head+i)%len(l.buf)])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	meta := LogMeta{
		Capacity:    len(l.buf),
		Dropped:     l.dropped,
		Count:       len(out),
		SeqVerified: true,
	}
	if len(out) > 0 {
		meta.FirstSeq = out[0].Seq
		meta.LastSeq = out[len(out)-1].Seq
	}
	return out, meta
}
