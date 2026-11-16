package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
)

// FrameType is the HTTP/2 frame type code for PRIORITY_UPDATE (RFC 9218, Section 4).
const FrameType = 0x10

const (
	defaultUrgency = 3
	maxUrgency     = 7

	// maxPending is the maximum number of priority entries kept for streams
	// that have not been opened yet.
	maxPending = 64
)

var (
	// ErrConnectionProtocol reports a PRIORITY_UPDATE frame that violates an
	// HTTP/2 connection-level rule: a truncated payload, a stream identifier
	// of zero, an even stream identifier (request streams are client-initiated
	// and therefore odd), or a set R (reserved) bit. The frame loop must treat
	// it as a connection error of type PROTOCOL_ERROR.
	ErrConnectionProtocol = errors.New("netsec: PRIORITY_UPDATE connection protocol error")

	// ErrFieldValue reports a Priority Field Value that is not a valid Structured
	// Fields dictionary, or that carries a recognized key ("u" or "i") with a
	// value of the wrong type or outside the allowed range. The offending update
	// is discarded without mutating any stored priority state.
	ErrFieldValue = errors.New("netsec: malformed PRIORITY_UPDATE field value")
)

// Priority is the RFC 9218 priority state associated with one stream.
type Priority struct {
	// Urgency is the "u" parameter, an integer in the range 0..7.
	Urgency uint8

	// Incremental is the "i" parameter: true marks the stream as incremental.
	Incremental bool
}

// DefaultPriority returns the default priority defined by RFC 9218:
// urgency 3, non-incremental.
func DefaultPriority() Priority {
	return Priority{Urgency: defaultUrgency}
}

// State keeps the per-connection RFC 9218 priority state. It is safe for
// concurrent use by the frame processing loop and stream schedulers.
//
// Entries for streams that have not been opened yet are capped at
// maxPending; when the cap is reached the oldest pending entry is evicted.
// Closing a stream drops its entry, and any later PRIORITY_UPDATE for that
// stream identifier is ignored for the lifetime of the connection.
type State struct {
	mu sync.Mutex

	// pending holds priorities for streams that received PRIORITY_UPDATE
	// before being opened. order records insertion order (oldest first) and
	// contains no duplicates: updating an existing pending entry refreshes
	// its value without changing its age.
	pending map[uint32]Priority
	order   []uint32

	// open holds priorities of streams that have been opened.
	open map[uint32]Priority

	// closed records stream identifiers whose streams have been closed and
	// whose identifiers are above closedWatermark. Every odd identifier at or
	// below closedWatermark is known to be closed as well, which keeps the
	// set bounded instead of growing for the lifetime of the connection.
	closed          map[uint32]struct{}
	closedWatermark uint32
}

// NewState returns an empty priority state.
func NewState() *State {
	return &State{
		pending: make(map[uint32]Priority),
		open:    make(map[uint32]Priority),
		closed:  make(map[uint32]struct{}),
	}
}

// UpdateFrame applies a complete PRIORITY_UPDATE frame payload: the 4-byte
// big-endian stream identifier followed by the Priority Field Value.
//
// It returns ErrConnectionProtocol for a truncated payload or an invalid
// stream identifier, and ErrFieldValue for a field value that cannot be
// parsed or validated. Updates for closed streams are accepted silently and
// ignored, as required by RFC 9218.
func (s *State) UpdateFrame(payload []byte) error {
	if len(payload) < 4 {
		return fmt.Errorf("%w: payload shorter than 4 bytes", ErrConnectionProtocol)
	}
	streamID := binary.BigEndian.Uint32(payload[:4])
	return s.Update(streamID, string(payload[4:]))
}

// Update applies a Priority Field Value to the given stream. It is the entry
// point used by UpdateFrame and can be called directly with an already
// decoded stream identifier.
func (s *State) Update(streamID uint32, fieldValue string) error {
	if err := validateStreamID(streamID); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isClosedLocked(streamID) {
		// A closed stream never re-enters state; everything addressed to it
		// is ignored, including a malformed field value.
		return nil
	}

	// Parse before touching any state so that a bad field value never leaves
	// a partial or default entry behind.
	priority, err := parsePriority(fieldValue)
	if err != nil {
		return err
	}

	if _, ok := s.open[streamID]; ok {
		s.open[streamID] = priority
		return nil
	}
	if _, ok := s.pending[streamID]; ok {
		// Refresh in place; the entry keeps its original position in order.
		s.pending[streamID] = priority
		return nil
	}

	if len(s.pending) >= maxPending {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.pending, oldest)
	}
	s.pending[streamID] = priority
	s.order = append(s.order, streamID)
	return nil
}

// StreamOpened marks streamID as open. A priority stored while the stream was
// pending is adopted; otherwise the stream starts with the default priority.
func (s *State) StreamOpened(streamID uint32) {
	if err := validateStreamID(streamID); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	priority, pending := s.pending[streamID]
	if pending {
		delete(s.pending, streamID)
		s.removeFromOrderLocked(streamID)
	} else {
		priority = DefaultPriority()
	}
	delete(s.closed, streamID)
	s.open[streamID] = priority
}

// StreamClosed drops any priority entry for streamID and records the
// identifier as closed so that later updates are ignored.
func (s *State) StreamClosed(streamID uint32) {
	if err := validateStreamID(streamID); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.open, streamID)
	if _, ok := s.pending[streamID]; ok {
		delete(s.pending, streamID)
		s.removeFromOrderLocked(streamID)
	}

	s.closed[streamID] = struct{}{}
	// Collapse every closed odd identifier starting from the lowest one into
	// the watermark so the closed set cannot grow without bound.
	for {
		next := s.closedWatermark + 2
		if _, ok := s.closed[next]; !ok {
			break
		}
		delete(s.closed, next)
		s.closedWatermark = next
	}
}

// Priority returns the stored priority for streamID and whether an entry
// exists. Missing and closed streams report ok=false.
func (s *State) Priority(streamID uint32) (Priority, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if priority, ok := s.open[streamID]; ok {
		return priority, true
	}
	if priority, ok := s.pending[streamID]; ok {
		return priority, true
	}
	return Priority{}, false
}

// EffectivePriority returns the stored priority for streamID, or the default
// priority when the stream has no entry (for example it was never mentioned
// in a PRIORITY_UPDATE frame, or its last update was discarded).
func (s *State) EffectivePriority(streamID uint32) Priority {
	if priority, ok := s.Priority(streamID); ok {
		return priority
	}
	return DefaultPriority()
}

// PendingCount returns the number of entries stored for unopened streams.
func (s *State) PendingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}

func (s *State) isClosedLocked(streamID uint32) bool {
	if streamID <= s.closedWatermark {
		return true
	}
	_, ok := s.closed[streamID]
	return ok
}

func (s *State) removeFromOrderLocked(streamID uint32) {
	for i, id := range s.order {
		if id == streamID {
			s.order = append(s.order[:i], s.order[i+1:]...)
			return
		}
	}
}

func validateStreamID(streamID uint32) error {
	// Request streams are client-initiated, so their identifiers are odd.
	// Zero denotes the connection itself and the high bit is the reserved R
	// bit, which must be zero.
	if streamID == 0 || streamID&1 == 0 || streamID > math.MaxInt32 {
		return fmt.Errorf("%w: invalid stream identifier %d", ErrConnectionProtocol, streamID)
	}
	return nil
}

// parsePriority parses a Priority Field Value as a Structured Fields
// dictionary (RFC 8941) and validates the recognized keys defined by
// RFC 9218: "u" is an integer in 0..7 and "i" is a boolean. Unknown keys are
// ignored and duplicate keys use the last occurrence. A type error on a
// recognized key, or an urgency outside 0..7, discards the whole field
// value; the caller therefore keeps whatever state it already had and
// lookups fall back to the default priority.
func parsePriority(fieldValue string) (Priority, error) {
	members, err := parseSFDictionary(fieldValue)
	if err != nil {
		return Priority{}, err
	}

	priority := Priority{Urgency: defaultUrgency}
	for key, member := range members {
		switch key {
		case "u":
			if member.item.kind != sfInteger {
				return Priority{}, fmt.Errorf("%w: key \"u\" must be an integer", ErrFieldValue)
			}
			urgency := member.item.integer
			if urgency < 0 || urgency > maxUrgency {
				return Priority{}, fmt.Errorf("%w: urgency %d out of range 0..7", ErrFieldValue, urgency)
			}
			priority.Urgency = uint8(urgency)
		case "i":
			if member.item.kind != sfBoolean {
				return Priority{}, fmt.Errorf("%w: key \"i\" must be a boolean", ErrFieldValue)
			}
			priority.Incremental = member.item.boolean
		}
	}

	return priority, nil
}
