package proto

// DefaultTable is the explicit state-transition table for a peer that
// answers a handshake and then echoes data frames.
//
//	Init         + Hello     -> Handshaking, emit HelloAck
//	Handshaking  + HelloAck  -> Established
//	Handshaking  + Timeout   -> Closed
//	Handshaking  + Unknown   -> Handshaking (counted, ignored)
//	Handshaking  + Reenter   -> Handshaking (counted, ignored)
//	Established  + Data      -> Established, emit ack
//	Established  + Ping      -> Established, emit Pong
//	Established  + Pong      -> Established
//	Established  + Close     -> Closed, emit Close
//	Established  + Timeout   -> Closed, emit Close
//	Established  + Unknown   -> Established (counted, ignored)
//	Established  + Reenter   -> Established (counted, ignored)
//	Init         + Unknown   -> Init (counted, ignored)
//
// Anything not listed (e.g. Data in Init/Handshaking) has no transition:
// business frames before the handshake completes are rejected.
func DefaultTable() []Transition {
	emit := func(typ MsgType, seq uint32, payload []byte) Action {
		return func(m *Machine, _ Frame) error {
			b, err := Encode(typ, seq, payload)
			if err != nil {
				return err
			}
			m.Emit(b)
			return nil
		}
	}
	ackData := func(m *Machine, f Frame) error {
		b, err := Encode(MsgData, f.Seq, append([]byte("ack:"), f.Payload...))
		if err != nil {
			return err
		}
		m.Emit(b)
		return nil
	}
	noop := func(*Machine, Frame) error { return nil }

	return []Transition{
		{StInit, EvHello, StHandshaking, emit(MsgHelloAck, 0, nil)},
		{StInit, EvUnknown, StInit, noop},

		{StHandshaking, EvHelloAck, StEstablished, noop},
		{StHandshaking, EvTimeout, StClosed, noop},
		{StHandshaking, EvUnknown, StHandshaking, noop},
		{StHandshaking, EvReenter, StHandshaking, noop},

		{StEstablished, EvData, StEstablished, ackData},
		{StEstablished, EvPing, StEstablished, emit(MsgPong, 0, nil)},
		{StEstablished, EvPong, StEstablished, noop},
		{StEstablished, EvClose, StClosed, emit(MsgClose, 0, nil)},
		{StEstablished, EvTimeout, StClosed, emit(MsgClose, 0, nil)},
		{StEstablished, EvUnknown, StEstablished, noop},
		{StEstablished, EvReenter, StEstablished, noop},
	}
}
