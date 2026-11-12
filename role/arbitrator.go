package role

import "sync"

// Sentinel errors returned for lifecycle mistakes rather than protocol
// violations. They never correspond to OpenFlow error messages.
var (
	ErrUnknownController = roleErr{"role: unknown controller id"}
	ErrAlreadyConnected  = roleErr{"role: controller already connected"}
)

type roleErr struct{ s string }

func (e roleErr) Error() string { return e.s }

// connState is everything the arbitrator remembers per controller
// connection.
type connState struct {
	role Role
	// gen is the generation id of the last accepted ROLE_REQUEST. The
	// first request on a fresh connection is always accepted.
	gen int64
	// seenGen records whether a generation id has been negotiated. It is
	// reset when the connection drops and reconnects.
	seenGen bool
	queue   *msgQueue
}

// Arbitrator serializes role arbitration across all controller
// connections of a switch proxy. Its methods are safe for concurrent use;
// a single mutex protects the role table and every per-controller queue.
type Arbitrator struct {
	mu     sync.Mutex
	conns  map[ControllerID]*connState
	master ControllerID
}

// NewArbitrator creates an empty arbitrator. Controllers register with
// Connect before any of their messages are dispatched.
func NewArbitrator() *Arbitrator {
	return &Arbitrator{conns: make(map[ControllerID]*connState)}
}

// Connect registers a freshly established controller connection. A new
// connection starts as OFPCR_ROLE_EQUAL with no negotiated generation id,
// matching the initial role of an OpenFlow switch-controller channel.
func (a *Arbitrator) Connect(id ControllerID) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.conns[id]; ok {
		return ErrAlreadyConnected
	}
	a.conns[id] = &connState{role: RoleEqual, queue: &msgQueue{}}
	return nil
}

// Disconnect removes a controller. Its queues are discarded and any
// master reservation it held is released so a reconnecting controller can
// negotiate the role again.
func (a *Arbitrator) Disconnect(id ControllerID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.conns[id]; !ok {
		return
	}
	if a.master == id {
		a.master = ""
	}
	delete(a.conns, id)
}

// HandleRoleRequest applies an OpenFlow 1.3 ROLE_REQUEST for controller id
// and returns the decision used to build ROLE_REPLY. Stale requests, bad
// role values and role conflicts are returned as *RoleError carrying the
// OFPET_ROLE_REQUEST_FAILED code; the stored state is left untouched.
//
// Generation ids are compared by their 64-bit signed difference, which is
// wraparound-safe: reqGen is older exactly when storedGen-reqGen is
// strictly positive, even if the unsigned values have wrapped through zero.
func (a *Arbitrator) HandleRoleRequest(id ControllerID, requested Role, reqGen int64) (*RoleDecision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	conn, ok := a.conns[id]
	if !ok {
		return nil, ErrUnknownController
	}
	if requested > RoleSlave {
		return nil, &RoleError{
			Type: ErrTypeRoleRequest,
			Code: ErrCodeUnsupported,
			Msg:  "unsupported controller role value",
		}
	}

	// The first ROLE_REQUEST on a connection is always accepted even when
	// it carries a "zero" generation id; afterwards the signed difference
	// decides. This mirrors a controller reconnecting with a reset id.
	if conn.seenGen && conn.gen-reqGen > 0 {
		return nil, &RoleError{
			Type: ErrTypeRoleRequest,
			Code: ErrCodeStale,
			Msg:  "stale role request generation id",
		}
	}
	conn.gen = reqGen
	conn.seenGen = true

	decision := &RoleDecision{
		Controller: id,
		Generation: reqGen,
	}

	switch requested {
	case RoleNochange:
		decision.Role = conn.role
	case RoleEqual:
		// Becoming equal releases a master reservation held by this
		// controller; equal controllers never force demotions.
		if conn.role == RoleMaster && a.master == id {
			a.master = ""
			decision.Demoted = append(decision.Demoted, id)
			conn.queue.markInvalidated()
		}
		conn.role = RoleEqual
		decision.Role = RoleEqual
	case RoleMaster:
		// At most one master exists. Granting master demotes the current
		// master (possibly the requestor itself) to slave before the
		// requestor takes over, and invalidates the old master's queued
		// writes at the barrier watermark.
		if a.master != "" && a.master != id {
			old := a.conns[a.master]
			old.role = RoleSlave
			old.queue.markInvalidated()
			decision.Demoted = append(decision.Demoted, a.master)
		}
		if conn.role == RoleMaster && a.master == id {
			// Re-requesting master refreshes the generation id but is not
			// itself a demotion.
			decision.Role = RoleMaster
			break
		}
		conn.role = RoleMaster
		a.master = id
		decision.Role = RoleMaster
	case RoleSlave:
		// A master demoting itself must invalidate its own queued writes.
		if conn.role == RoleMaster {
			if a.master == id {
				a.master = ""
			}
			conn.queue.markInvalidated()
			decision.Demoted = append(decision.Demoted, id)
		}
		conn.role = RoleSlave
		decision.Role = RoleSlave
	}

	return decision, nil
}

// IsStateModifying reports whether msgType is one of the controller-mutable
// OpenFlow messages (FLOW_MOD, PACKET_OUT, METER_MOD and their siblings)
// that slave controllers are forbidden to send.
func IsStateModifying(msgType uint8) bool {
	switch msgType {
	case TypeFlowMod, TypeGroupMod, TypePortMod, TypeTableMod,
		TypePacketOut, TypeMeterMod:
		return true
	default:
		return false
	}
}

// Enqueue admits an inbound controller message into its connection's
// ordered queue. Slave controllers are rejected up front for
// state-modifying messages; everything else is queued and later judged in
// order by Next. Rejection is returned as *RoleError with OFPRRFC_BAD_ROLE
// semantics (the proxy maps it to OFPET_BAD_REQUEST / OFPBRC_EPERM).
func (a *Arbitrator) Enqueue(id ControllerID, env *Envelope) error {
	if env == nil {
		return roleErr{"role: nil envelope"}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	conn, ok := a.conns[id]
	if !ok {
		return ErrUnknownController
	}
	if conn.role == RoleSlave && IsStateModifying(env.MsgType) {
		return &RoleError{
			Type: ErrTypeRoleRequest,
			Code: ErrCodeBadRole,
			Msg:  "slave controller must not send state-modifying messages",
		}
	}
	env.Controller = id
	env.Verdict = VerdictDeliver
	conn.queue.push(env)
	return nil
}

// Next dequeues the oldest message of a controller in connection order.
// Envelopes invalidated by a role demotion are returned with
// VerdictDrop so the dispatcher can free them in barrier order; all other
// envelopes carry VerdictDeliver. It returns nil when the queue is empty.
func (a *Arbitrator) Next(id ControllerID) *Envelope {
	a.mu.Lock()
	defer a.mu.Unlock()
	conn, ok := a.conns[id]
	if !ok {
		return nil
	}
	return conn.queue.pop()
}

// Pending reports how many queued envelopes a controller still has.
func (a *Arbitrator) Pending(id ControllerID) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	conn, ok := a.conns[id]
	if !ok {
		return 0
	}
	return conn.queue.len()
}

// AsyncRecipients returns the controllers that must receive an
// asynchronous switch message (for example PACKET_IN, FLOW_REMOVED,
// PORT_STATUS). Per OpenFlow 1.3 only master and equal controllers are
// eligible; slave controllers never receive asynchronous messages. The
// result is sorted for deterministic fan-out.
func (a *Arbitrator) AsyncRecipients() []ControllerID {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]ControllerID, 0, len(a.conns))
	for id, conn := range a.conns {
		if conn.role == RoleMaster || conn.role == RoleEqual {
			out = append(out, id)
		}
	}
	sortIDs(out)
	return out
}

// Role returns the current role of a controller.
func (a *Arbitrator) Role(id ControllerID) (Role, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	conn, ok := a.conns[id]
	if !ok {
		return 0, false
	}
	return conn.role, true
}

// Master returns the current master controller, or "" when no controller
// holds the master role.
func (a *Arbitrator) Master() ControllerID {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.master
}

// Generation returns the generation id last negotiated by a controller.
func (a *Arbitrator) Generation(id ControllerID) (int64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	conn, ok := a.conns[id]
	if !ok {
		return 0, false
	}
	return conn.gen, true
}

// sortIDs orders controller ids lexicographically without pulling in sort
// package allocation patterns at call sites.
func sortIDs(ids []ControllerID) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
}
