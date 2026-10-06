// Package fragmentagg aggregates keyed, sequence-numbered byte fragments.
//
// Fragments for a key arrive in any order via Add. An explicit End call
// declares the maximum sequence number for the key; End may arrive before
// any fragment. Once every sequence number from 0 through the declared
// maximum is present, the fragments are concatenated in order and
// returned, and all staged state for the key is released.
//
// Duplicate fragments are accepted only when their payloads are identical;
// a differing payload for an already-staged sequence number is a conflict.
// A fragment whose sequence number exceeds the declared maximum, and an
// End whose maximum is contradicted by an earlier End or by an
// already-staged fragment, are also conflicts.
//
// The aggregator enforces a quota measured in staged bytes: the sum of
// payload bytes currently buffered across all keys. Adds that would
// exceed the quota are rejected and nothing is staged.
package fragmentagg

import (
	"bytes"
	"errors"
	"fmt"
)

// ErrLimitExceeded is returned when staging a fragment would push the
// total staged bytes beyond the aggregator's quota.
var ErrLimitExceeded = errors.New("fragmentagg: staged byte limit exceeded")

// ConflictError describes an inconsistent fragment or End declaration.
type ConflictError struct {
	Key    string
	Seq    uint64
	Reason string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("fragmentagg: conflict for key %q seq %d: %s", e.Key, e.Seq, e.Reason)
}

type stream struct {
	frags  map[uint64][]byte
	bytes  int
	max    uint64
	hasEnd bool
}

// Aggregator collects fragments for many keys concurrently. It is not
// safe for concurrent use; callers must serialize access.
type Aggregator struct {
	limit   int
	staged  int
	streams map[string]*stream
}

// New returns an Aggregator that stages at most limit payload bytes
// across all keys. A negative limit means no quota.
func New(limit int) *Aggregator {
	return &Aggregator{limit: limit, streams: make(map[string]*stream)}
}

// StagedBytes reports the payload bytes currently buffered across all keys.
func (a *Aggregator) StagedBytes() int { return a.staged }

// PendingKeys reports how many keys currently hold staged state.
func (a *Aggregator) PendingKeys() int { return len(a.streams) }

// Add stages one fragment for key. If the fragment completes the stream
// (End already declared and 0..max now all present), it returns the
// assembled payload with complete set to true and releases the key's
// state. Identical re-adds of a staged fragment are accepted as no-ops.
func (a *Aggregator) Add(key string, seq uint64, payload []byte) (out []byte, complete bool, err error) {
	s := a.streams[key]
	if s == nil {
		s = &stream{frags: make(map[uint64][]byte)}
		a.streams[key] = s
	}

	if s.hasEnd && seq > s.max {
		return nil, false, &ConflictError{Key: key, Seq: seq,
			Reason: fmt.Sprintf("sequence exceeds declared maximum %d", s.max)}
	}

	if prev, ok := s.frags[seq]; ok {
		if !bytes.Equal(prev, payload) {
			return nil, false, &ConflictError{Key: key, Seq: seq,
				Reason: "duplicate fragment with different payload"}
		}
		return s.tryComplete(a, key)
	}

	if a.limit >= 0 && a.staged+len(payload) > a.limit {
		return nil, false, ErrLimitExceeded
	}

	cp := make([]byte, len(payload))
	copy(cp, payload)
	s.frags[seq] = cp
	s.bytes += len(cp)
	a.staged += len(cp)

	return s.tryComplete(a, key)
}

// End declares that max is the highest sequence number for key. It may be
// called before any fragment arrives. Repeating End with the same maximum
// is accepted; a different maximum, or a maximum below an already-staged
// fragment, is a conflict. If the declaration completes the stream, End
// returns the assembled payload and releases the key's state.
func (a *Aggregator) End(key string, max uint64) (out []byte, complete bool, err error) {
	s := a.streams[key]
	if s == nil {
		s = &stream{frags: make(map[uint64][]byte)}
		a.streams[key] = s
	}

	if s.hasEnd {
		if s.max != max {
			return nil, false, &ConflictError{Key: key, Seq: max,
				Reason: fmt.Sprintf("end redeclared with maximum %d, already %d", max, s.max)}
		}
		return s.tryComplete(a, key)
	}

	for seq := range s.frags {
		if seq > max {
			return nil, false, &ConflictError{Key: key, Seq: seq,
				Reason: fmt.Sprintf("staged fragment exceeds declared maximum %d", max)}
		}
	}

	s.max = max
	s.hasEnd = true
	return s.tryComplete(a, key)
}

// tryComplete assembles and releases the stream when End has been
// declared and every sequence number 0..max is staged. Because staged
// sequence numbers are unique and never exceed max, count == max+1 is
// exactly the contiguous-complete condition.
func (s *stream) tryComplete(a *Aggregator, key string) ([]byte, bool, error) {
	if !s.hasEnd || uint64(len(s.frags)) != s.max+1 {
		return nil, false, nil
	}
	var out []byte
	for i := uint64(0); i <= s.max; i++ {
		out = append(out, s.frags[i]...)
	}
	a.staged -= s.bytes
	delete(a.streams, key)
	return out, true, nil
}
