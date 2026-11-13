// Package prioritystate maintains RFC 9218 client-priority state for the
// streams of one HTTP/2 connection.
//
// It is designed to be called from the connection's frame processing loop:
//
//   - ParseFramePayload splits a PRIORITY_UPDATE frame (frame type 0x10) into
//     its stream identifier and Priority Field Value.
//   - HandleUpdate applies one parsed update. A zero or even stream identifier
//     yields a connection error (ErrProtocolError); malformed field values are
//     rejected without mutating any state.
//   - StreamOpened and StreamClosed track stream lifecycle so that updates for
//     not-yet-opened streams are bounded (at most 64 pending entries) and
//     updates for closed streams are ignored.
//
// All methods are safe for concurrent use.
package prioritystate

import (
	"errors"
	"sync"
)

// FrameTypePriorityUpdate is the HTTP/2 frame type for PRIORITY_UPDATE
// (RFC 9218, section 6.2).
const FrameTypePriorityUpdate = 0x10

// MaxPendingEntries bounds the number of priority entries retained for streams
// that have never been opened.
const MaxPendingEntries = 64

// Stream status stored per tracked stream identifier.
const (
	statusPending = iota // update received before the stream was opened
	statusOpen           // stream is active
	statusClosed         // stream closed or reset; further updates are ignored
)

var (
	// ErrProtocolError indicates a condition that the frame loop must treat as
	// a connection error of type PROTOCOL_ERROR.
	ErrProtocolError = errors.New("prioritystate: PROTOCOL_ERROR")

	// ErrMalformedField indicates that the Priority Field Value is not a valid
	// structured-fields dictionary or that a recognized key has the wrong type
	// or value. The caller ignores the frame; no state is changed.
	ErrMalformedField = errors.New("prioritystate: malformed priority field value")

	errShortPayload = errors.New("prioritystate: PRIORITY_UPDATE payload shorter than 4 bytes")
)

// ConnError wraps a condition that aborts the HTTP/2 connection.
type ConnError struct {
	err error
}

func (e *ConnError) Error() string { return e.err.Error() }
func (e *ConnError) Unwrap() error { return e.err }

// Entry is the effective priority state of one stream.
type Entry struct {
	// Urgency is the "u" parameter, 0 (most urgent) through 7 (least urgent).
	Urgency int

	// Incremental is the "i" parameter.
	Incremental bool
}

// DefaultEntry is the priority assigned when no valid PRIORITY_UPDATE has
// been observed: urgency 3, non-incremental.
var DefaultEntry = Entry{Urgency: 3, Incremental: false}

type streamState struct {
	entry  Entry
	status int
}

// State holds per-connection priority state.
type State struct {
	mu sync.Mutex

	// streams tracks every referenced client stream identifier. Closed
	// identifiers keep a statusClosed tombstone so that later updates can be
	// ignored while the set of tracked identifiers stays bounded.
	streams map[uint32]*streamState

	// pending is the FIFO order of not-yet-opened stream identifiers; its
	// length never exceeds MaxPendingEntries.
	pending []uint32

	// highestSeen is the greatest client stream identifier ever observed.
	highestSeen uint32

	// lowestOpened is the smallest identifier ever opened. Client stream
	// identifiers are strictly increasing, so every identifier below this one
	// belongs to a stream that can never open and must not retain state.
	lowestOpened uint32
}

// New returns an empty State.
func New() *State {
	return &State{streams: make(map[uint32]*streamState)}
}

// HandleFrame applies a complete PRIORITY_UPDATE frame payload: a 4-byte
// stream identifier followed by the Priority Field Value. A returned
// *ConnError must be surfaced as a connection error.
func (s *State) HandleFrame(payload []byte) error {
	streamID, fieldValue, err := ParseFramePayload(payload)
	if err != nil {
		return &ConnError{err: err}
	}
	return s.HandleUpdate(streamID, fieldValue)
}

// ParseFramePayload splits a PRIORITY_UPDATE frame payload into the stream
// identifier and the raw Priority Field Value.
func ParseFramePayload(payload []byte) (streamID uint32, fieldValue []byte, err error) {
	if len(payload) < 4 {
		return 0, nil, errShortPayload
	}
	// The R reserved bit is not part of the stream identifier.
	streamID = (uint32(payload[0])&0x7f)<<24 |
		uint32(payload[1])<<16 |
		uint32(payload[2])<<8 |
		uint32(payload[3])
	return streamID, payload[4:], nil
}

// HandleUpdate applies one PRIORITY_UPDATE.
func (s *State) HandleUpdate(streamID uint32, fieldValue []byte) error {
	if err := validateClientStreamID(streamID); err != nil {
		return &ConnError{err: err}
	}

	entry, ok := parsePriorityFieldValue(fieldValue)
	if !ok {
		// The whole field value is discarded; do not create or modify state.
		return ErrMalformedField
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if streamID > s.highestSeen {
		s.highestSeen = streamID
	}

	if st, exists := s.streams[streamID]; exists {
		if st.status == statusClosed {
			// Stream closed: discard the entry and ignore later updates.
			return nil
		}
		st.entry = entry // refresh open entry, or pending entry in place
		return nil
	}

	// Drop entries for streams that were made impossible by an opened stream.
	s.purgeStalePendingLocked()

	// The stream identifier is behind an already opened stream: the stream
	// can never open, so the update is ignored without allocating state.
	if s.lowestOpened != 0 && streamID < s.lowestOpened {
		return nil
	}

	if len(s.pending) >= MaxPendingEntries {
		oldest := s.pending[0]
		s.pending = s.pending[1:]
		delete(s.streams, oldest)
	}
	s.streams[streamID] = &streamState{entry: entry, status: statusPending}
	s.pending = append(s.pending, streamID)
	return nil
}

// StreamOpened marks a stream open. Any pending update for it becomes the
// stream's effective priority; otherwise the default priority is used.
func (s *State) StreamOpened(streamID uint32) (Entry, error) {
	if err := validateClientStreamID(streamID); err != nil {
		return DefaultEntry, &ConnError{err: err}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if streamID > s.highestSeen {
		s.highestSeen = streamID
	}
	if s.lowestOpened == 0 || streamID < s.lowestOpened {
		s.lowestOpened = streamID
	}

	if st, exists := s.streams[streamID]; exists {
		switch st.status {
		case statusClosed:
			// Identifiers are never reused.
			return DefaultEntry, &ConnError{err: ErrProtocolError}
		case statusOpen:
			return st.entry, nil
		default:
			st.status = statusOpen
			s.removePendingLocked(streamID)
			return st.entry, nil
		}
	}

	s.streams[streamID] = &streamState{entry: DefaultEntry, status: statusOpen}
	return DefaultEntry, nil
}

// StreamClosed discards a stream's entry. Subsequent PRIORITY_UPDATE frames for
// the stream are ignored. It is a no-op for unknown streams.
func (s *State) StreamClosed(streamID uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if streamID > s.highestSeen {
		s.highestSeen = streamID
	}
	if st, exists := s.streams[streamID]; exists {
		st.status = statusClosed
		st.entry = DefaultEntry
		s.removePendingLocked(streamID)
	}
}

// Priority returns the effective priority of an open stream and reports
// whether the stream is currently tracked as open.
func (s *State) Priority(streamID uint32) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, ok := s.streams[streamID]
	if !ok || st.status != statusOpen {
		return DefaultEntry, false
	}
	return st.entry, true
}

// PendingCount returns the number of retained entries for not-yet-opened
// streams. Intended for tests and metrics.
func (s *State) PendingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}

// purgeStalePendingLocked drops pending entries whose identifiers are below
// the smallest opened identifier and therefore can never open.
func (s *State) purgeStalePendingLocked() {
	if len(s.pending) == 0 {
		return
	}
	kept := s.pending[:0]
	for _, id := range s.pending {
		if s.lowestOpened != 0 && id < s.lowestOpened {
			delete(s.streams, id)
			continue
		}
		kept = append(kept, id)
	}
	s.pending = kept
}

func (s *State) removePendingLocked(streamID uint32) {
	for idx, id := range s.pending {
		if id == streamID {
			s.pending = append(s.pending[:idx], s.pending[idx+1:]...)
			return
		}
	}
}

// validateClientStreamID enforces that request streams use odd, non-zero
// identifiers. Zero or even identifiers are connection errors.
func validateClientStreamID(streamID uint32) error {
	if streamID == 0 || streamID%2 == 0 {
		return ErrProtocolError
	}
	return nil
}

// parsePriorityFieldValue parses the Priority Field Value dictionary.
//
// "u" must be an integer from 0 through 7 and "i" must be a boolean. Unknown
// keys are ignored and duplicate keys take the last value. A type error on a
// recognized key, an out-of-range urgency, or any structural parse error makes
// the whole field value invalid; the caller keeps existing state.
func parsePriorityFieldValue(fieldValue []byte) (Entry, bool) {
	members, ok := parseDictionary(fieldValue)
	if !ok {
		return DefaultEntry, false
	}

	entry := DefaultEntry
	for _, member := range members {
		switch member.key {
		case "u":
			if member.item.typ != sfInteger {
				return DefaultEntry, false
			}
			u := member.item.intV
			if u < 0 || u > 7 {
				return DefaultEntry, false
			}
			entry.Urgency = int(u)
		case "i":
			if member.item.typ != sfBoolean {
				return DefaultEntry, false
			}
			entry.Incremental = member.item.boolV
		default:
			// Unknown keys are ignored.
		}
	}
	return entry, true
}
