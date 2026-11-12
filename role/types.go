// Package role implements OpenFlow 1.3 controller-role arbitration for a
// switch proxy that is simultaneously connected to several controllers.
//
// The package is intended to be called by the proxy's message dispatcher.
// It owns the per-controller role state, the generation-id comparison used
// by ROLE_REQUEST handling, the write-admission checks for slave
// controllers, the recipient set for asynchronous messages and the
// barrier-ordered invalidation of messages that a demoted master queued
// before the role change.
package role

// ControllerID identifies a controller connection of the proxy. A proxy
// normally has one connection per controller.
type ControllerID string

// Role is an OpenFlow 1.3 controller role, as carried in a
// ofp_role_prop_role property of ROLE_REQUEST / ROLE_REPLY.
type Role uint32

const (
	// RoleNochange corresponds to OFPCR_ROLE_NOCHANGE: the controller only
	// refreshes its generation id without changing its current role.
	RoleNochange Role = 0
	// RoleEqual corresponds to OFPCR_ROLE_EQUAL. Equal controllers have
	// full write access and receive asynchronous messages.
	RoleEqual Role = 1
	// RoleMaster corresponds to OFPCR_ROLE_MASTER. There is at most one
	// master; granting master demotes any current master to slave.
	RoleMaster Role = 2
	// RoleSlave corresponds to OFPCR_ROLE_SLAVE. Slave controllers have
	// read-only access and do not receive asynchronous messages.
	RoleSlave Role = 3
)

// String returns the OpenFlow name of the role.
func (r Role) String() string {
	switch r {
	case RoleNochange:
		return "OFPCR_ROLE_NOCHANGE"
	case RoleEqual:
		return "OFPCR_ROLE_EQUAL"
	case RoleMaster:
		return "OFPCR_ROLE_MASTER"
	case RoleSlave:
		return "OFPCR_ROLE_SLAVE"
	default:
		return "OFPCR_ROLE_UNKNOWN"
	}
}

// OpenFlow 1.3.4 error type and codes used when a ROLE_REQUEST is rejected
// (see section 7.5.4 "Controller roles"). The dispatcher maps these onto
// an OFPET_ROLE_REQUEST_FAILED error message.
const (
	// ErrTypeRoleRequest is OFPET_ROLE_REQUEST_FAILED.
	ErrTypeRoleRequest uint16 = 11

	// ErrCodeStale is OFPRRFC_STALE: the request generation id is older
	// than the generation id known to the switch.
	ErrCodeStale uint16 = 0
	// ErrCodeUnsupported is OFPRRFC_UNSUPPORTED: the requested role value
	// is not defined by the protocol.
	ErrCodeUnsupported uint16 = 1
	// ErrCodeBadRole is OFPRRFC_BAD_ROLE: the role request is otherwise
	// invalid (for example, another controller already holds master).
	ErrCodeBadRole uint16 = 2
)

// OpenFlow 1.3.4 controller-mutable message types (ofp_type values) that a
// slave controller is not permitted to send. Only these state-modifying
// types require admission; read and statistics messages stay allowed.
const (
	TypeFlowMod     uint8 = 14 // OFPT_FLOW_MOD
	TypeGroupMod    uint8 = 15 // OFPT_GROUP_MOD
	TypePortMod     uint8 = 16 // OFPT_PORT_MOD
	TypeTableMod    uint8 = 17 // OFPT_TABLE_MOD
	TypePacketOut   uint8 = 13 // OFPT_PACKET_OUT
	TypeMeterMod    uint8 = 29 // OFPT_METER_MOD (OpenFlow 1.3.1+)
	TypeRoleRequest uint8 = 24 // OFPT_ROLE_REQUEST
)

// RoleError describes a rejected ROLE_REQUEST. It carries the OpenFlow
// error type and code so the dispatcher can build the error reply.
type RoleError struct {
	Type uint16
	Code uint16
	Msg  string
}

func (e *RoleError) Error() string {
	return "role: " + e.Msg
}

// Verdict is the result of admitting an enqueued message when it is
// dequeued for delivery to the switch.
type Verdict int

const (
	// VerdictDeliver forwards the message to the datapath.
	VerdictDeliver Verdict = iota
	// VerdictDrop discards the message because it was queued by a master
	// that has since been demoted. The drop happens in queue order.
	VerdictDrop
)

// Envelope is one queued controller message plus its admission result.
type Envelope struct {
	// Controller is the connection the message arrived on.
	Controller ControllerID
	// MsgType is the OpenFlow ofp_type of the carried message.
	MsgType uint8
	// Xid is the OpenFlow transaction id, used for barrier bookkeeping.
	Xid uint32
	// Payload is the raw OpenFlow message; the arbitrator does not parse it.
	Payload []byte
	// Verdict is populated by Next: deliver, or drop after a demotion.
	Verdict Verdict
}

// RoleDecision is the accepted outcome of a ROLE_REQUEST.
type RoleDecision struct {
	// Controller whose role was requested.
	Controller ControllerID
	// Role is the effective role after applying the request.
	Role Role
	// Generation is the generation id now stored for the controller.
	Generation int64
	// Demoted lists controllers that lost master in this transition and
	// whose pre-transition queued writes must be invalidated. The
	// requesting controller is included when it demoted itself.
	Demoted []ControllerID
}
