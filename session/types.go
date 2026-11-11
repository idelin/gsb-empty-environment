package session

import (
	"fmt"
	"net/netip"
	"time"
)

// Direction is the direction of a flow relative to the gateway.
type Direction uint8

const (
	// DirectionUnknown is the zero value and is rejected by the engine.
	DirectionUnknown Direction = 0
	// DirectionIngress is traffic entering the protected network.
	DirectionIngress Direction = 1
	// DirectionEgress is traffic leaving the protected network.
	DirectionEgress Direction = 2
)

// String returns a stable textual representation of the direction.
func (d Direction) String() string {
	switch d {
	case DirectionIngress:
		return "ingress"
	case DirectionEgress:
		return "egress"
	default:
		return "unknown"
	}
}

// FlowID is the transport-level identity of a flow. It deliberately contains
// no policy-generation information.
type FlowID struct {
	Protocol uint8
	SrcAddr  netip.Addr
	SrcPort  uint16
	DstAddr  netip.Addr
	DstPort  uint16
}

// Valid reports whether the flow identifier is well formed. Port numbers are
// not checked because protocols such as ICMP do not use ports.
func (f FlowID) Valid() bool {
	return f.Protocol != 0 && f.SrcAddr.IsValid() && f.DstAddr.IsValid()
}

// String renders the transport identity of the flow.
func (f FlowID) String() string {
	return fmt.Sprintf("%d/%s:%d -> %s:%d",
		f.Protocol,
		f.SrcAddr,
		f.SrcPort,
		f.DstAddr,
		f.DstPort,
	)
}

// Tuple identifies a single session. It extends FlowID with the direction and,
// critically, the policy generation at which the session was started. Two
// connections reusing the same transport 5-tuple therefore have distinct
// tuples and never share an authorization.
type Tuple struct {
	Flow       FlowID
	Direction  Direction
	StartupGen uint64
}

// String renders the tuple including its pinned startup generation.
func (t Tuple) String() string {
	return fmt.Sprintf("%s %d/%s:%d -> %s:%d gen=%d",
		t.Direction,
		t.Flow.Protocol,
		t.Flow.SrcAddr,
		t.Flow.SrcPort,
		t.Flow.DstAddr,
		t.Flow.DstPort,
		t.StartupGen,
	)
}

// State is the lifecycle state of a tracked session.
type State uint8

const (
	// StateNone is the zero value.
	StateNone State = iota
	// StateNew means the connection is tracked but no packet was observed yet.
	StateNew
	// StateEstablished means at least one packet has been authorized.
	StateEstablished
	// StateClosed means the session ended normally.
	StateClosed
	// StateRevoked means the session was revoked by a policy update.
	StateRevoked
)

// IsActive reports whether packets may still be authorized in this state.
func (s State) IsActive() bool {
	return s == StateNew || s == StateEstablished
}

// String returns a stable textual representation of the state.
func (s State) String() string {
	switch s {
	case StateNew:
		return "new"
	case StateEstablished:
		return "established"
	case StateClosed:
		return "closed"
	case StateRevoked:
		return "revoked"
	default:
		return "none"
	}
}

// Decision is the authorization verdict for a connection or a packet.
type Decision uint8

const (
	// DecisionDeny rejects the traffic.
	DecisionDeny Decision = iota
	// DecisionAllow permits the traffic.
	DecisionAllow
)

// String returns a stable textual representation of the decision.
func (d Decision) String() string {
	switch d {
	case DecisionAllow:
		return "allow"
	default:
		return "deny"
	}
}

// EventKind identifies the kind of record stored in the engine event log.
type EventKind uint8

const (
	// EventPolicyUpdate marks a policy generation change.
	EventPolicyUpdate EventKind = iota + 1
	// EventConnect marks a connection establishment attempt.
	EventConnect
	// EventFirstPacket marks the first authorized packet on a session.
	EventFirstPacket
	// EventEnd marks the normal close of a session.
	EventEnd
	// EventRevoke marks the revocation of a session by an update.
	EventRevoke
)

// String returns a stable textual representation of the event kind.
func (k EventKind) String() string {
	switch k {
	case EventPolicyUpdate:
		return "policy_update"
	case EventConnect:
		return "connect"
	case EventFirstPacket:
		return "first_packet"
	case EventEnd:
		return "end"
	case EventRevoke:
		return "revoke"
	default:
		return "unknown"
	}
}

// Event is one entry in the engine's linear event log.
type Event struct {
	// Seq is the one-based dispatch order assigned by the engine.
	Seq int64
	// At is the injected clock reading when the event was recorded.
	At time.Time
	// Kind is the event type.
	Kind EventKind
	// Tuple is the affected session tuple; it is the zero value for
	// EventPolicyUpdate.
	Tuple Tuple
	// Gen is the policy generation in effect after the event. For session
	// events it equals Tuple.StartupGen.
	Gen uint64
	// Decision is the verdict recorded with EventConnect; it is the zero
	// value for every other kind.
	Decision Decision
}
