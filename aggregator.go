// Package fragagg reassembles sequence-numbered fragments per key.
//
// Fragments arrive in any order via Add. An explicit Finish call declares
// the maximum sequence number for a key; once every sequence from 0 through
// that maximum is staged, the concatenated payload is emitted and all
// staged state for the key is released. Finish may arrive before, between,
// or after the fragments.
//
// Duplicate deliveries of the same sequence are accepted only when the
// payload is byte-identical. Staging a sequence above the declared maximum,
// or declaring two different maxima, is a conflict. Memory pressure is
// bounded by a quota measured in staged payload bytes.
package fragagg

import (
	"bytes"
	"errors"
	"fmt"
)

// ErrQuotaExceeded is returned when staging a fragment would push the
// aggregator past its staged-byte quota. Nothing is staged in that case.
var ErrQuotaExceeded = errors.New("fragagg: staged-byte quota exceeded")

// ConflictError reports inconsistent input for a key: a re-delivered
// fragment with different bytes, a sequence beyond the declared maximum,
// or two different declared maxima.
type ConflictError struct {
	Key    string
	Reason string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("fragagg: conflict on key %q: %s", e.Key, e.Reason)
}

type stream struct {
	frags  map[uint64][]byte
	end    uint64
	hasEnd bool
	bytes  int
}

// Aggregator collects fragments for many keys under a shared byte quota.
type Aggregator struct {
	quota   int
	staged  int
	streams map[string]*stream
}

// New returns an Aggregator that holds at most quota staged payload bytes
// across all keys. A negative quota means unlimited.
func New(quota int) *Aggregator {
	return &Aggregator{quota: quota, streams: make(map[string]*stream)}
}

// StagedBytes reports the payload bytes currently held across all keys.
func (a *Aggregator) StagedBytes() int { return a.staged }

// Add stages one fragment for key. When the fragment completes the key
// (maximum declared and every sequence 0..max present), it returns the
// assembled payload with done=true and releases the key's state.
func (a *Aggregator) Add(key string, seq uint64, payload []byte) (out []byte, done bool, err error) {
	s := a.streams[key]
	if s == nil {
		s = &stream{frags: make(map[uint64][]byte)}
		a.streams[key] = s
	}
	if s.hasEnd && seq > s.end {
		return nil, false, &ConflictError{Key: key, Reason: fmt.Sprintf("sequence %d exceeds declared maximum %d", seq, s.end)}
	}
	if prev, ok := s.frags[seq]; ok {
		if !bytes.Equal(prev, payload) {
			return nil, false, &ConflictError{Key: key, Reason: fmt.Sprintf("sequence %d re-delivered with different bytes", seq)}
		}
		return nil, false, nil
	}
	if a.quota >= 0 && a.staged+len(payload) > a.quota {
		return nil, false, ErrQuotaExceeded
	}
	s.frags[seq] = payload
	s.bytes += len(payload)
	a.staged += len(payload)
	return a.tryEmit(key, s)
}

// Finish declares maxSeq as the highest sequence number for key. It may be
// called before any fragment arrives. When the key is complete it returns
// the assembled payload with done=true and releases the key's state.
func (a *Aggregator) Finish(key string, maxSeq uint64) (out []byte, done bool, err error) {
	s := a.streams[key]
	if s == nil {
		s = &stream{frags: make(map[uint64][]byte)}
		a.streams[key] = s
	}
	if s.hasEnd {
		if s.end != maxSeq {
			return nil, false, &ConflictError{Key: key, Reason: fmt.Sprintf("maximum re-declared as %d, was %d", maxSeq, s.end)}
		}
		return a.tryEmit(key, s)
	}
	for seq := range s.frags {
		if seq > maxSeq {
			return nil, false, &ConflictError{Key: key, Reason: fmt.Sprintf("staged sequence %d exceeds declared maximum %d", seq, maxSeq)}
		}
	}
	s.end = maxSeq
	s.hasEnd = true
	return a.tryEmit(key, s)
}

// Reset discards all staged state for key, freeing its quota.
func (a *Aggregator) Reset(key string) {
	if s := a.streams[key]; s != nil {
		a.staged -= s.bytes
		delete(a.streams, key)
	}
}

// tryEmit emits the assembled payload when the stream is complete: the
// maximum is declared and every sequence 0..max is staged. Because staged
// sequences are distinct and never exceed the maximum, the count check
// guarantees contiguity. The count is compared as len-1 so that a maximum
// of math.MaxUint64 (unreachable in practice) cannot overflow the check.
func (a *Aggregator) tryEmit(key string, s *stream) ([]byte, bool, error) {
	if !s.hasEnd || len(s.frags) == 0 || uint64(len(s.frags))-1 != s.end {
		return nil, false, nil
	}
	var out []byte
	total := 0
	for _, p := range s.frags {
		total += len(p)
	}
	out = make([]byte, 0, total)
	for i := uint64(0); ; i++ {
		out = append(out, s.frags[i]...)
		if i == s.end {
			break
		}
	}
	a.staged -= s.bytes
	delete(a.streams, key)
	return out, true, nil
}
