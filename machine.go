package proto

import (
	"errors"
	"fmt"
)

type State int

const (
	StInit State = iota
	StHandshaking
	StEstablished
	StClosed
)

func (s State) String() string {
	switch s {
	case StInit:
		return "Init"
	case StHandshaking:
		return "Handshaking"
	case StEstablished:
		return "Established"
	case StClosed:
		return "Closed"
	default:
		return fmt.Sprintf("State(%d)", int(s))
	}
}

// Event is the machine-level input. Unknown message types, idle timeouts
// and re-entrant handshake attempts are lifted into their own events so
// the table handles them independently of the normal message flow.
type Event int

const (
	EvHello Event = iota
	EvHelloAck
	EvData
	EvPing
	EvPong
	EvClose
	EvUnknown // frame carried an unknown message type
	EvTimeout // idle timeout fired by the clock, not by the wire
	EvReenter // duplicate handshake attempt / re-entrant input
)

func (e Event) String() string {
	switch e {
	case EvHello:
		return "Hello"
	case EvHelloAck:
		return "HelloAck"
	case EvData:
		return "Data"
	case EvPing:
		return "Ping"
	case EvPong:
		return "Pong"
	case EvClose:
		return "Close"
	case EvUnknown:
		return "Unknown"
	case EvTimeout:
		return "Timeout"
	case EvReenter:
		return "Reenter"
	default:
		return fmt.Sprintf("Event(%d)", int(e))
	}
}

var (
	// ErrNoTransition: the (state, event) pair has no table entry. The
	// state is left untouched. Business frames before the handshake
	// completes hit exactly this path.
	ErrNoTransition = errors.New("no transition for state/event")
	// ErrReentrant: an action tried to re-enter Apply while a transition
	// was already in flight.
	ErrReentrant = errors.New("reentrant Apply rejected")
)

// Action produces output frames as a side effect of a transition. If an
// Action returns an error, the transition is aborted: the state stays and
// any outputs the action emitted are discarded (atomicity).
type Action func(m *Machine, f Frame) error

// Transition is one row of the explicit state-transition table.
type Transition struct {
	From   State
	On     Event
	To     State
	Action Action
}

type tableKey struct {
	from  State
	event Event
}

// Machine executes a compiled transition table.
type Machine struct {
	state  State
	table  map[tableKey]Transition
	parser Parser

	outputs [][]byte  // committed outputs only
	pending *[][]byte // scratch buffer of the in-flight transition

	expected uint32 // next acceptable Data sequence number (dedup rule)

	// Statistics make the independent event paths observable.
	Unknown    int // unknown message types seen
	Duplicates int // replayed Data frames dropped by the dedup rule
	Reentries  int // re-entrant handshake attempts ignored

	inApply bool
}

// Compile validates the explicit transition table and returns a Machine
// ready to run it. Duplicate rows and out-of-range states/events are
// compile-time (i.e. construction-time) errors.
func Compile(entries []Transition) (*Machine, error) {
	table := make(map[tableKey]Transition, len(entries))
	for i, tr := range entries {
		if tr.From < StInit || tr.From > StClosed {
			return nil, fmt.Errorf("row %d: invalid from-state %d", i, tr.From)
		}
		if tr.To < StInit || tr.To > StClosed {
			return nil, fmt.Errorf("row %d: invalid to-state %d", i, tr.To)
		}
		if tr.On < EvHello || tr.On > EvReenter {
			return nil, fmt.Errorf("row %d: invalid event %d", i, tr.On)
		}
		k := tableKey{tr.From, tr.On}
		if _, dup := table[k]; dup {
			return nil, fmt.Errorf("row %d: duplicate transition %s + %s", i, tr.From, tr.On)
		}
		if tr.Action == nil {
			tr.Action = func(*Machine, Frame) error { return nil }
		}
		table[k] = tr
	}
	return &Machine{state: StInit, table: table, expected: 1}, nil
}

func (m *Machine) State() State { return m.state }

// Emit queues an output frame. It only commits if the surrounding
// transition succeeds.
func (m *Machine) Emit(frame []byte) {
	if m.pending != nil {
		*m.pending = append(*m.pending, frame)
	}
}

// DrainOutputs returns and clears the committed output frames.
func (m *Machine) DrainOutputs() [][]byte {
	out := m.outputs
	m.outputs = nil
	return out
}

// Apply executes one transition atomically: the action runs first; only
// on success are its outputs committed and the state switched. On action
// failure the old state is kept and pending outputs are dropped.
func (m *Machine) Apply(ev Event, f Frame) error {
	if m.inApply {
		return ErrReentrant
	}
	tr, ok := m.table[tableKey{m.state, ev}]
	if !ok {
		return fmt.Errorf("%w: %s + %s", ErrNoTransition, m.state, ev)
	}
	m.inApply = true
	var pending [][]byte
	m.pending = &pending
	err := tr.Action(m, f)
	m.pending = nil
	m.inApply = false
	if err != nil {
		return fmt.Errorf("action for %s + %s failed: %w", m.state, ev, err)
	}
	m.outputs = append(m.outputs, pending...)
	m.state = tr.To
	return nil
}

// eventFor maps a decoded frame to a machine event. Unknown types and
// re-entrant handshakes become their own events instead of being errors.
func (m *Machine) eventFor(f Frame) Event {
	switch f.Type {
	case MsgHello:
		if m.state != StInit {
			return EvReenter // handshake already attempted/completed
		}
		return EvHello
	case MsgHelloAck:
		return EvHelloAck
	case MsgData:
		return EvData
	case MsgPing:
		return EvPing
	case MsgPong:
		return EvPong
	case MsgClose:
		return EvClose
	default:
		return EvUnknown
	}
}

// Dispatch routes one decoded frame into the machine. Data frames first
// pass the explicit dedup rule:
//
//	seq == expected -> accept, run the transition, expected++
//	seq <  expected -> replay: drop payload, re-emit the ack, count it
//	seq >  expected -> gap: protocol error, state untouched
func (m *Machine) Dispatch(f Frame) error {
	ev := m.eventFor(f)
	switch ev {
	case EvUnknown:
		m.Unknown++
	case EvReenter:
		m.Reentries++
	case EvData:
		switch {
		case f.Seq < m.expected:
			m.Duplicates++
			ack, err := Encode(MsgData, f.Seq, []byte("dup-ack"))
			if err != nil {
				return err
			}
			m.outputs = append(m.outputs, ack)
			return nil
		case f.Seq > m.expected:
			return fmt.Errorf("sequence gap: got %d, want %d", f.Seq, m.expected)
		}
		if err := m.Apply(ev, f); err != nil {
			return err
		}
		m.expected++ // only after the transition committed
		return nil
	}
	return m.Apply(ev, f)
}

// Feed ingests one arbitrary chunk of bytes, decodes every complete frame
// and dispatches it. It returns nil on ErrNeedMoreData (a partial frame
// simply waits for the next chunk) and propagates *BadDataError.
func (m *Machine) Feed(b []byte) error {
	m.parser.Feed(b)
	for {
		f, err := m.parser.Next()
		if errors.Is(err, ErrNeedMoreData) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := m.Dispatch(f); err != nil {
			return err
		}
	}
}

// OnTimeout injects the independent timeout event (called by the clock,
// not by wire input).
func (m *Machine) OnTimeout() error { return m.Apply(EvTimeout, Frame{}) }
