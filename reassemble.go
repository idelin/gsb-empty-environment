// Package reassemble reassembles fragmented messages keyed by message ID,
// total length and fragment offset. Overlapping fragments must agree
// byte-for-byte, otherwise a conflict is reported. It enforces per-message
// and global byte quotas (checked before allocation), fragment expiration,
// and deduplication of fragments arriving after completion.
package reassemble

import (
	"errors"
	"time"
)

var (
	// ErrConflict is returned when overlapping fragments disagree on content.
	ErrConflict = errors.New("reassemble: overlapping fragments disagree")
	// ErrTotalLenMismatch is returned when fragments of one message ID
	// disagree on the total length.
	ErrTotalLenMismatch = errors.New("reassemble: fragments disagree on total length")
	// ErrMessageTooLarge is returned when total length exceeds the
	// per-message quota. Checked before any buffer is allocated.
	ErrMessageTooLarge = errors.New("reassemble: message exceeds per-message quota")
	// ErrGlobalQuota is returned when the global buffered-bytes quota would
	// be exceeded. Checked before any buffer is allocated.
	ErrGlobalQuota = errors.New("reassemble: global quota exceeded")
	// ErrInvalidFragment is returned for negative offsets/lengths or a
	// fragment reaching past the declared total length.
	ErrInvalidFragment = errors.New("reassemble: fragment outside message bounds")
)

// Config tunes a Reassembler.
type Config struct {
	// MaxMessageSize is the per-message quota: the largest acceptable
	// total length in bytes.
	MaxMessageSize int
	// MaxBufferedBytes is the global quota: the largest sum of buffers
	// held by all in-progress messages.
	MaxBufferedBytes int
	// TTL is how long an incomplete message is kept before expiring.
	TTL time.Duration
	// CompletionTTL is how long a completed message ID is remembered for
	// deduplication of late fragments.
	CompletionTTL time.Duration
}

type segment struct{ start, end int }

type message struct {
	totalLen int
	buf      []byte
	segs     []segment // sorted, non-overlapping covered ranges
	covered  int
	deadline time.Time
}

// Reassembler holds the state of all in-progress and recently completed
// messages. It is not safe for concurrent use.
type Reassembler struct {
	cfg      Config
	now      func() time.Time
	msgs     map[uint64]*message
	done     map[uint64]time.Time // completed ID -> dedup deadline
	buffered int
}

// New creates a Reassembler. If now is nil, time.Now is used; tests may
// inject a fake clock.
func New(cfg Config, now func() time.Time) *Reassembler {
	if now == nil {
		now = time.Now
	}
	return &Reassembler{
		cfg:  cfg,
		now:  now,
		msgs: make(map[uint64]*message),
		done: make(map[uint64]time.Time),
	}
}

// Add feeds one fragment: payload p of the message id, starting at offset,
// where totalLen is the declared total message length. Fragments may arrive
// in any order and may overlap or repeat; overlapping bytes must be
// identical or ErrConflict is returned.
//
// It returns (data, true, nil) exactly once, when the message becomes
// complete; data is a freshly allocated copy. Fragments for an already
// completed ID are deduplicated and return (nil, true, nil).
func (r *Reassembler) Add(id uint64, totalLen, offset int, p []byte) ([]byte, bool, error) {
	if totalLen < 0 || offset < 0 || offset+len(p) > totalLen {
		return nil, false, ErrInvalidFragment
	}
	now := r.now()
	if deadline, ok := r.done[id]; ok {
		if now.Before(deadline) {
			return nil, true, nil // duplicate of a completed message
		}
		delete(r.done, id)
	}
	m, ok := r.msgs[id]
	if ok && !now.Before(m.deadline) {
		// Expired: drop the old buffer. A late fragment must start a
		// fresh message instead of borrowing the stale buffer.
		r.buffered -= m.totalLen
		delete(r.msgs, id)
		ok = false
	}
	if !ok {
		// Quota checks happen before allocating the message buffer.
		if totalLen > r.cfg.MaxMessageSize {
			return nil, false, ErrMessageTooLarge
		}
		if r.buffered+totalLen > r.cfg.MaxBufferedBytes {
			return nil, false, ErrGlobalQuota
		}
		m = &message{
			totalLen: totalLen,
			buf:      make([]byte, totalLen),
			deadline: now.Add(r.cfg.TTL),
		}
		r.msgs[id] = m
		r.buffered += totalLen
	} else if m.totalLen != totalLen {
		return nil, false, ErrTotalLenMismatch
	}
	if len(p) > 0 {
		if err := m.add(offset, p); err != nil {
			return nil, false, err
		}
	}
	if m.covered == m.totalLen {
		data := make([]byte, m.totalLen)
		copy(data, m.buf)
		r.buffered -= m.totalLen
		delete(r.msgs, id)
		r.done[id] = now.Add(r.cfg.CompletionTTL)
		return data, true, nil
	}
	return nil, false, nil
}

// add verifies overlapping bytes against buf, copies p in, and merges the
// covered interval set.
func (m *message) add(off int, p []byte) error {
	end := off + len(p)
	for _, s := range m.segs {
		if s.end <= off || s.start >= end {
			continue
		}
		lo := max(off, s.start)
		hi := min(end, s.end)
		for i := lo; i < hi; i++ {
			if m.buf[i] != p[i-off] {
				return ErrConflict
			}
		}
	}
	copy(m.buf[off:end], p)

	merged := segment{off, end}
	var out []segment
	inserted := false
	for _, s := range m.segs {
		switch {
		case s.end < merged.start:
			out = append(out, s)
		case merged.end < s.start:
			if !inserted {
				out = append(out, merged)
				inserted = true
			}
			out = append(out, s)
		default:
			merged.start = min(merged.start, s.start)
			merged.end = max(merged.end, s.end)
		}
	}
	if !inserted {
		out = append(out, merged)
	}
	m.segs = out

	covered := 0
	for _, s := range m.segs {
		covered += s.end - s.start
	}
	m.covered = covered
	return nil
}

// Expire drops all expired in-progress messages (freeing their quota) and
// all stale completion-dedup entries. It returns the number of in-progress
// messages dropped. Add already lazily expires entries it touches; Expire
// sweeps the rest.
func (r *Reassembler) Expire() int {
	now := r.now()
	dropped := 0
	for id, m := range r.msgs {
		if !now.Before(m.deadline) {
			r.buffered -= m.totalLen
			delete(r.msgs, id)
			dropped++
		}
	}
	for id, deadline := range r.done {
		if !now.Before(deadline) {
			delete(r.done, id)
		}
	}
	return dropped
}

// Buffered reports the bytes currently held by in-progress messages.
func (r *Reassembler) Buffered() int { return r.buffered }

// InFlight reports the number of in-progress messages.
func (r *Reassembler) InFlight() int { return len(r.msgs) }
