package fsm

import (
	"errors"
	"time"
)

// Independent events for inputs outside the happy path.
const (
	EventHello   Event = "HELLO"
	EventPing    Event = "PING"
	EventData    Event = "DATA"
	EventBye     Event = "BYE"
	EventUnknown Event = "UNKNOWN" // unknown message type byte
	EventTimeout Event = "TIMEOUT" // injected by Tick after idle timeout
	EventReenter Event = "REENTER" // handshake message after handshake
)

// ErrReentrantCall is returned when an action re-enters the session while
// a transition is still running.
var ErrReentrantCall = errors.New("session: re-entrant Handle while a transition runs")

// Session couples a Parser with a Machine and enforces the protocol rules:
//
//   - Business frames (PING/DATA/BYE) before the handshake completes hit
//     no table row and are rejected with *NoTransitionError.
//   - Action output and the state change commit atomically; a failed
//     action keeps the old state.
//   - Dedup rule (explicit): a DATA frame whose sequence number is <= the
//     last APPLIED sequence is a replay. Replays are answered with the
//     cached output of the original execution; the action is NOT re-run
//     and the state does NOT change. Sequences need not be contiguous.
//   - Unknown type bytes, idle timeouts and handshake re-entry are
//     dispatched as the independent events UNKNOWN, TIMEOUT and REENTER.
type Session struct {
	machine *Machine
	state   State
	parser  *Parser

	busy bool // re-entrancy guard

	lastSeq uint32
	hasSeq  bool
	lastOut interface{}

	timeout      time.Duration
	lastActivity time.Time
	now          func() time.Time
}

// NewSession builds a session in the machine's initial state.
func NewSession(m *Machine, maxFrame int, timeout time.Duration) *Session {
	now := time.Now
	return &Session{
		machine:      m,
		state:        m.Initial(),
		parser:       NewParser(maxFrame),
		timeout:      timeout,
		lastActivity: now(),
		now:          now,
	}
}

// State returns the current state.
func (s *Session) State() State { return s.state }

// SetClock overrides the time source (used by tests and embedders).
func (s *Session) SetClock(now func() time.Time) {
	s.now = now
	s.lastActivity = now()
}

// Feed appends one arbitrary chunk of wire bytes.
func (s *Session) Feed(b []byte) { s.parser.Feed(b) }

// Buffered reports how many undecoded bytes are held.
func (s *Session) Buffered() int { return s.parser.Buffered() }

// Next parses and dispatches the next buffered frame. It returns
// ErrNeedMoreData when the buffer is incomplete and *BadFrameError when
// the bytes are corrupt.
func (s *Session) Next() (*Result, error) {
	f, err := s.parser.Next()
	if err != nil {
		return nil, err
	}
	r := s.Handle(f)
	return &r, nil
}

// Handle maps one frame to an event and runs one atomic transition.
func (s *Session) Handle(f *Frame) Result {
	if s.busy {
		return Result{Err: ErrReentrantCall}
	}
	s.busy = true
	defer func() { s.busy = false }()

	s.lastActivity = s.now()

	var ev Event
	switch f.Type {
	case TypeHello:
		if s.state == s.machine.Initial() {
			ev = EventHello
		} else {
			ev = EventReenter // handshake re-entry is its own event
		}
	case TypePing:
		ev = EventPing
	case TypeData:
		// Explicit dedup rule (see Session comment).
		if s.hasSeq && f.Seq <= s.lastSeq {
			return Result{Output: s.lastOut, Deduplicated: true}
		}
		ev = EventData
	case TypeBye:
		ev = EventBye
	default:
		ev = EventUnknown // unknown message types are their own event
	}

	next, res := s.machine.Step(s.state, ev, &Context{Event: ev, Frame: f})
	if res.Err != nil {
		return res // action failed or no transition: old state kept
	}
	// Commit output and state change atomically.
	s.state = next
	if ev == EventData {
		s.lastSeq = f.Seq
		s.hasSeq = true
		s.lastOut = res.Output
	}
	return res
}

// Tick injects the independent TIMEOUT event when the session has been
// idle for at least its configured timeout. It returns a zero Result when
// no timeout fired.
func (s *Session) Tick() Result {
	if s.timeout <= 0 || s.now().Sub(s.lastActivity) < s.timeout {
		return Result{}
	}
	s.lastActivity = s.now()
	next, res := s.machine.Step(s.state, EventTimeout, &Context{Event: EventTimeout})
	if res.Err != nil {
		return res
	}
	s.state = next
	return res
}
