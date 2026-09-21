package proto

import (
	"errors"
	"strings"
	"testing"
)

func newMachine(t *testing.T) *Machine {
	t.Helper()
	m, err := Compile(DefaultTable())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return m
}

func handshake(t *testing.T, m *Machine) {
	t.Helper()
	if err := m.Dispatch(Frame{Type: MsgHello}); err != nil {
		t.Fatalf("Hello: %v", err)
	}
	if err := m.Dispatch(Frame{Type: MsgHelloAck}); err != nil {
		t.Fatalf("HelloAck: %v", err)
	}
	if m.State() != StEstablished {
		t.Fatalf("state = %s, want Established", m.State())
	}
	m.DrainOutputs()
}

func TestCompileRejectsBadTable(t *testing.T) {
	row := Transition{StInit, EvHello, StHandshaking, nil}
	if _, err := Compile([]Transition{row, row}); err == nil {
		t.Fatal("duplicate rows should be a compile error")
	}
	if _, err := Compile([]Transition{{State(99), EvHello, StInit, nil}}); err == nil {
		t.Fatal("out-of-range state should be a compile error")
	}
	if _, err := Compile([]Transition{{StInit, Event(99), StInit, nil}}); err == nil {
		t.Fatal("out-of-range event should be a compile error")
	}
}

func TestHandshakeGatesBusinessFrames(t *testing.T) {
	for _, pre := range []struct {
		name  string
		state func(*Machine)
	}{
		{"Init", func(m *Machine) {}},
		{"Handshaking", func(m *Machine) {
			if err := m.Dispatch(Frame{Type: MsgHello}); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(pre.name, func(t *testing.T) {
			m := newMachine(t)
			pre.state(m)
			before := m.State()
			err := m.Dispatch(Frame{Type: MsgData, Seq: 1, Payload: []byte("x")})
			if !errors.Is(err, ErrNoTransition) {
				t.Fatalf("got %v, want ErrNoTransition", err)
			}
			if m.State() != before {
				t.Fatalf("state changed to %s on rejected frame", m.State())
			}
		})
	}
}

func TestHandshakeFlowAndOutputs(t *testing.T) {
	m := newMachine(t)
	if err := m.Dispatch(Frame{Type: MsgHello}); err != nil {
		t.Fatal(err)
	}
	if m.State() != StHandshaking {
		t.Fatalf("state = %s, want Handshaking", m.State())
	}
	outs := m.DrainOutputs()
	if len(outs) != 1 {
		t.Fatalf("got %d outputs, want 1 (HelloAck)", len(outs))
	}
	var p Parser
	p.Feed(outs[0])
	f, _ := p.Next()
	if f.Type != MsgHelloAck {
		t.Fatalf("output = %s, want HelloAck", f.Type)
	}
	if err := m.Dispatch(Frame{Type: MsgHelloAck}); err != nil {
		t.Fatal(err)
	}
	if m.State() != StEstablished {
		t.Fatalf("state = %s, want Established", m.State())
	}
}

func TestActionFailureKeepsOldState(t *testing.T) {
	boom := errors.New("boom")
	m, err := Compile([]Transition{
		{StInit, EvHello, StHandshaking, func(m *Machine, _ Frame) error {
			m.Emit([]byte("half-baked output")) // emitted before the failure
			return boom
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = m.Dispatch(Frame{Type: MsgHello})
	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want wrapped boom", err)
	}
	if m.State() != StInit {
		t.Fatalf("state = %s, want Init after failed action", m.State())
	}
	if outs := m.DrainOutputs(); len(outs) != 0 {
		t.Fatalf("failed action leaked %d outputs", len(outs))
	}
}

func TestDedupReplayRule(t *testing.T) {
	m := newMachine(t)
	handshake(t, m)

	// First delivery: accepted, acked.
	if err := m.Dispatch(Frame{Type: MsgData, Seq: 1, Payload: []byte("a")}); err != nil {
		t.Fatal(err)
	}
	if outs := m.DrainOutputs(); len(outs) != 1 {
		t.Fatalf("got %d outputs, want 1 ack", len(outs))
	}

	// Replay of the same input: payload dropped, ack re-emitted, counted.
	if err := m.Dispatch(Frame{Type: MsgData, Seq: 1, Payload: []byte("a")}); err != nil {
		t.Fatal(err)
	}
	if m.Duplicates != 1 {
		t.Fatalf("Duplicates = %d, want 1", m.Duplicates)
	}
	outs := m.DrainOutputs()
	if len(outs) != 1 {
		t.Fatalf("replay should re-emit exactly 1 ack, got %d", len(outs))
	}
	var p Parser
	p.Feed(outs[0])
	f, _ := p.Next()
	if f.Seq != 1 || !strings.HasPrefix(string(f.Payload), "dup-ack") {
		t.Fatalf("replay ack = seq %d %q", f.Seq, f.Payload)
	}

	// Gap: seq 3 while expecting 2 is a protocol error, state untouched.
	if err := m.Dispatch(Frame{Type: MsgData, Seq: 3}); err == nil {
		t.Fatal("sequence gap should be an error")
	}
	if m.State() != StEstablished {
		t.Fatalf("state = %s after gap, want Established", m.State())
	}

	// The next legal seq is still 2: the gap did not advance the rule.
	if err := m.Dispatch(Frame{Type: MsgData, Seq: 2, Payload: []byte("b")}); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownMessageIsIndependentEvent(t *testing.T) {
	m := newMachine(t)
	handshake(t, m)
	if err := m.Dispatch(Frame{Type: MsgType(0x7f)}); err != nil {
		t.Fatalf("unknown message should be handled, got %v", err)
	}
	if m.Unknown != 1 {
		t.Fatalf("Unknown = %d, want 1", m.Unknown)
	}
	if m.State() != StEstablished {
		t.Fatalf("state = %s, want Established", m.State())
	}
}

func TestTimeoutIsIndependentEvent(t *testing.T) {
	// Timeout during handshake closes the machine.
	m := newMachine(t)
	if err := m.Dispatch(Frame{Type: MsgHello}); err != nil {
		t.Fatal(err)
	}
	if err := m.OnTimeout(); err != nil {
		t.Fatal(err)
	}
	if m.State() != StClosed {
		t.Fatalf("state = %s, want Closed", m.State())
	}

	// Timeout while established emits Close and closes.
	m2 := newMachine(t)
	handshake(t, m2)
	if err := m2.OnTimeout(); err != nil {
		t.Fatal(err)
	}
	if m2.State() != StClosed {
		t.Fatalf("state = %s, want Closed", m2.State())
	}
	if outs := m2.DrainOutputs(); len(outs) != 1 {
		t.Fatalf("timeout should emit Close, got %d outputs", len(outs))
	}
}

func TestReentryIsIndependentEvent(t *testing.T) {
	m := newMachine(t)
	handshake(t, m)
	// A second Hello after the handshake is a re-entrant attempt.
	if err := m.Dispatch(Frame{Type: MsgHello}); err != nil {
		t.Fatalf("re-entrant Hello should be handled, got %v", err)
	}
	if m.Reentries != 1 {
		t.Fatalf("Reentries = %d, want 1", m.Reentries)
	}
	if m.State() != StEstablished {
		t.Fatalf("state = %s, want Established", m.State())
	}
}

func TestReentrantApplyRejected(t *testing.T) {
	var inner error
	m, err := Compile([]Transition{
		{StInit, EvHello, StHandshaking, func(m *Machine, _ Frame) error {
			inner = m.Apply(EvTimeout, Frame{}) // re-enter while applying
			return inner
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Dispatch(Frame{Type: MsgHello}); !errors.Is(err, ErrReentrant) {
		t.Fatalf("got %v, want ErrReentrant", err)
	}
	if !errors.Is(inner, ErrReentrant) {
		t.Fatalf("inner Apply got %v, want ErrReentrant", inner)
	}
	if m.State() != StInit {
		t.Fatalf("state = %s, want Init", m.State())
	}
}

func TestFeedChunkedEndToEnd(t *testing.T) {
	m := newMachine(t)
	hello := encodeMust(t, MsgHello, 0, nil)
	ack := encodeMust(t, MsgHelloAck, 0, nil)
	data := encodeMust(t, MsgData, 1, []byte("chunked"))
	wire := append(append(append([]byte{}, hello...), ack...), data...)
	// Split at awkward boundaries: 1 byte, then 2, then the rest.
	if err := m.Feed(wire[:1]); err != nil {
		t.Fatal(err)
	}
	if err := m.Feed(wire[1:3]); err != nil {
		t.Fatal(err)
	}
	if err := m.Feed(wire[3:]); err != nil {
		t.Fatal(err)
	}
	if m.State() != StEstablished {
		t.Fatalf("state = %s, want Established", m.State())
	}
	if outs := m.DrainOutputs(); len(outs) != 2 { // HelloAck + data ack
		t.Fatalf("got %d outputs, want 2", len(outs))
	}
}

func TestFeedBadDataPropagates(t *testing.T) {
	m := newMachine(t)
	err := m.Feed([]byte{0xff, 0xff, byte(MsgHello)})
	var bde *BadDataError
	if !errors.As(err, &bde) {
		t.Fatalf("got %v, want *BadDataError", err)
	}
	if m.State() != StInit {
		t.Fatalf("state = %s, want Init", m.State())
	}
}
