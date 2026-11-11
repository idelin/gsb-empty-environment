// Package conntrack implements a stateful TCP connection tracker for a
// userspace firewall.
//
// The tracker maintains, for every connection, the acceptable sequence-number
// range in each direction together with the window-scale factor negotiated in
// the handshake. RST segments only tear down a connection when their sequence
// number matches the receiver's next expected sequence exactly; an RST whose
// sequence lies inside the receive window does not kill the connection and
// instead produces a VerdictChallengeACK so the firewall can emit a challenge
// acknowledgment. Out-of-window data and bogus control segments are dropped.
//
// The state machine handles the ordinary three-way handshake, simultaneous
// open, half closes, SYN retransmission (including SYN segments arriving on an
// established connection) and 32-bit sequence-number wrap-around.
package conntrack

import "net/netip"

// TCP control bits as carried on the wire.
const (
	FlagFIN uint8 = 1 << 0
	FlagSYN uint8 = 1 << 1
	FlagRST uint8 = 1 << 2
	FlagPSH uint8 = 1 << 3
	FlagACK uint8 = 1 << 4
	FlagURG uint8 = 1 << 5
)

// Verdict is the firewall decision for a single segment.
type Verdict int

const (
	// VerdictDrop means the segment must not be delivered.
	VerdictDrop Verdict = iota
	// VerdictAccept means the segment is valid for its connection and may
	// be delivered.
	VerdictAccept
	// VerdictChallengeACK means the segment is suspicious (typically an RST
	// or SYN with an in-window sequence, or an unacceptable ACK) and must
	// not be delivered; the firewall should instead send a challenge ACK
	// back to the segment source.
	VerdictChallengeACK
)

// String returns the conventional name of the verdict.
func (v Verdict) String() string {
	switch v {
	case VerdictAccept:
		return "ACCEPT"
	case VerdictChallengeACK:
		return "CHALLENGE_ACK"
	default:
		return "DROP"
	}
}

// State is the connection-level state tracked by the middlebox. The names
// follow the conventional TCP state machine; they describe what has been
// observed in either direction rather than the state of a single endpoint.
type State int

const (
	StateUnknown State = iota
	// StateSynSent: a SYN opened the connection, no SYN from the peer yet.
	StateSynSent
	// StateSynRcvd: both sides have exchanged SYNs, final ACK outstanding.
	StateSynRcvd
	// StateEstablished: the handshake completed in both directions.
	StateEstablished
	// StateFinWait1: one side has sent a FIN which has not been acknowledged.
	StateFinWait1
	// StateFinWait2: one FIN was acknowledged, waiting for the peer's FIN.
	StateFinWait2
	// StateCloseWait: a FIN arrived but the local side has not FINed yet.
	StateCloseWait
	// StateClosing: simultaneous FIN close, both FINs seen, ACK outstanding.
	StateClosing
	// StateLastAck: the side that received a first FIN has now sent its own.
	StateLastAck
	// StateTimeWait: both FINs have been fully acknowledged.
	StateTimeWait
)

// String returns the conventional name of the state.
func (s State) String() string {
	switch s {
	case StateSynSent:
		return "SYN_SENT"
	case StateSynRcvd:
		return "SYN_RCVD"
	case StateEstablished:
		return "ESTABLISHED"
	case StateFinWait1:
		return "FIN_WAIT_1"
	case StateFinWait2:
		return "FIN_WAIT_2"
	case StateCloseWait:
		return "CLOSE_WAIT"
	case StateClosing:
		return "CLOSING"
	case StateLastAck:
		return "LAST_ACK"
	case StateTimeWait:
		return "TIME_WAIT"
	default:
		return "UNKNOWN"
	}
}

// Packet describes one TCP segment handed to the tracker. Payload length is
// carried explicitly so the caller does not have to keep the payload around.
type Packet struct {
	SrcIP   netip.Addr
	DstIP   netip.Addr
	SrcPort uint16
	DstPort uint16
	Seq     uint32
	Ack     uint32
	Flags   uint8
	Window  uint16
	Payload int
	// WScale is the window-scale option value (0..14); -1 means the option
	// was absent. Only meaningful on SYN segments.
	WScale int
	// HasWScale reports whether a window-scale option was present.
	HasWScale bool
}

// Has reports whether all the given flags are set.
func (p Packet) Has(flags uint8) bool { return p.Flags&flags == flags }

// ConnKey identifies a connection independently of direction.
type ConnKey struct {
	IP0   netip.Addr
	Port0 uint16
	IP1   netip.Addr
	Port1 uint16
}

// key builds the canonical, direction-independent key for a packet.
func key(p Packet) ConnKey {
	lo := p.SrcIP.Less(p.DstIP) || (p.SrcIP == p.DstIP && p.SrcPort < p.DstPort)
	if lo {
		return ConnKey{p.SrcIP, p.SrcPort, p.DstIP, p.DstPort}
	}
	return ConnKey{p.DstIP, p.DstPort, p.SrcIP, p.SrcPort}
}
