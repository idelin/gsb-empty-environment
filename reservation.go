// Package netsec implements a SCSI-3 persistent reservation state machine for
// a storage target. It models the Write Exclusive - Registrants Only (WERO)
// reservation type and is driven by the target's persistent-reservation
// request dispatcher.
//
// Security model:
//
//   - A reservation key is only meaningful when bound to the transport port
//     identifier (initiator transport ID) that registered it. Every
//     privileged request is authenticated against that binding, so a key
//     stolen by a compromised node cannot be replayed from another port.
//   - PREEMPT is restricted to registrants that present the key registered on
//     the requesting port.
//   - CLEAR is restricted to ports named by the management policy.
//   - The reservation generation counter is bumped once for every successful
//     state-changing operation, giving clients a tamper/staleness check.
//   - APTPL (activate persist through power loss) controls which registrations
//     and whether the reservation survive a power loss.
package netsec

import (
	"errors"
	"sort"
	"sync"
)

// PortID is the initiator transport port identifier (e.g. a SAS address, an FC
// port name or an iSCSI name). The target derives it from the transport layer;
// clients cannot assert an arbitrary port identifier.
type PortID string

// Key is an 8-byte persistent reservation key. Zero means "no key" and is used
// to unregister.
type Key uint64

// Opcode selects a persistent reservation service action.
type Opcode int

const (
	OpRegister Opcode = iota + 1
	OpReserve
	OpRelease
	OpPreempt
	OpPreemptAndAbort
	OpClear
	OpReadKeys
	OpReadReservation
)

// String returns the SPC service-action name of the opcode.
func (op Opcode) String() string {
	switch op {
	case OpRegister:
		return "REGISTER"
	case OpReserve:
		return "RESERVE"
	case OpRelease:
		return "RELEASE"
	case OpPreempt:
		return "PREEMPT"
	case OpPreemptAndAbort:
		return "PREEMPT AND ABORT"
	case OpClear:
		return "CLEAR"
	case OpReadKeys:
		return "READ KEYS"
	case OpReadReservation:
		return "READ RESERVATION"
	default:
		return "UNKNOWN"
	}
}

// Sentinel errors returned by the state machine. They wrap concrete *PRError
// values, so errors.Is works against both the sentinel and the code.
var (
	ErrNotRegistered       = errors.New("netsec: initiator port is not registered")
	ErrKeyMismatch         = errors.New("netsec: reservation key does not match the key bound to the initiator port")
	ErrKeyInUse            = errors.New("netsec: reservation key is already registered by another initiator port")
	ErrClearNotAuthorized  = errors.New("netsec: initiator port is not authorized by management policy to issue CLEAR")
	ErrReservationConflict = errors.New("netsec: persistent reservation conflict")
	ErrInvalidRequest      = errors.New("netsec: invalid persistent reservation request")
)

// Code classifies a state-machine failure.
type Code int

const (
	CodeOK Code = iota
	CodeNotRegistered
	CodeKeyMismatch
	CodeKeyInUse
	CodeClearNotAuthorized
	CodeReservationConflict
	CodeInvalidRequest
)

// PRError is the structured error returned for failed service actions. The
// dispatcher can map Code onto a SCSI sense key/ASC/ASCQ.
type PRError struct {
	Op   Opcode
	Code Code
	err  error
}

func (e *PRError) Error() string {
	return e.Op.String() + ": " + e.err.Error()
}

func (e *PRError) Unwrap() error { return e.err }

func prError(op Opcode, code Code, sentinel error) *PRError {
	return &PRError{Op: op, Code: code, err: sentinel}
}

// registration is a key binding: one key registered by exactly one initiator
// port. The same key on a different port is rejected, which is what stops a
// stolen key from being re-registered after the attacker moves ports.
type registration struct {
	key   Key
	aptpl bool
}

// ReservationInfo describes the current persistent reservation.
type ReservationInfo struct {
	Holder PortID
	Type   Opcode // always OpReserve; the reservation type is WERO
}

// Request is one persistent reservation request as decoded by the target. The
// target fills Source from the authenticated transport layer rather than from
// the command payload.
type Request struct {
	Op     Opcode
	Source PortID
	// Key is the reservation key the initiator authenticates with.
	Key Key
	// NewKey is the replacement key for OpRegister (zero unregisters the
	// source port).
	NewKey Key
	// PreemptKey identifies the registration to preempt.
	PreemptKey Key
	// APTPL is the activate-persist-through-power-loss bit used by OpRegister.
	APTPL bool
}

// Response reports the outcome of a successful service action.
type Response struct {
	Op Opcode
	// Generation is the reservation generation after the action completed.
	Generation uint64
	// Keys is populated for OpReadKeys.
	Keys []Key
	// Reservation is populated for OpReadReservation; nil when no reservation
	// is held.
	Reservation *ReservationInfo
	// Aborted is set for a successful OpPreemptAndAbort to indicate that the
	// preempted registrant's outstanding tasks must be aborted.
	Aborted bool
}

// Policy holds management-controlled authorization state.
type Policy struct {
	// ClearPorts are the only initiator ports allowed to issue CLEAR.
	ClearPorts map[PortID]bool
}

// Machine is the persistent reservation state machine for one logical unit.
// Its methods are safe for concurrent use by the request dispatcher.
type Machine struct {
	mu sync.RWMutex

	registrations map[PortID]*registration
	keyOwners     map[Key]PortID
	reservation   *ReservationInfo
	generation    uint64
	policy        Policy
}

// NewMachine creates an empty state machine. clearPorts are the
// management-authorized initiator ports permitted to clear registrations and
// the reservation.
func NewMachine(clearPorts ...PortID) *Machine {
	policy := Policy{ClearPorts: make(map[PortID]bool, len(clearPorts))}
	for _, port := range clearPorts {
		policy.ClearPorts[port] = true
	}
	return &Machine{
		registrations: make(map[PortID]*registration),
		keyOwners:     make(map[Key]PortID),
		policy:        policy,
	}
}

// Generation returns the current reservation generation counter.
func (m *Machine) Generation() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.generation
}

// Registered reports whether port currently holds a registration.
func (m *Machine) Registered(port PortID) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.registrations[port]
	return ok
}

// WriteAllowed implements the Write Exclusive - Registrants Only access
// control: when no reservation is held the LU is unreserved and any port may
// write; once reserved, only registered initiator ports may write. The target
// invokes this from its write-command path, not from the PR dispatcher.
func (m *Machine) WriteAllowed(port PortID) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.reservation == nil {
		return true
	}
	_, ok := m.registrations[port]
	return ok
}

// Register adds, replaces, or removes the registration belonging to req.Source.
//
//   - A new registration must present Key == 0 and a non-zero NewKey.
//   - An existing port must present the key currently bound to it and may move
//     to NewKey (key rotation), or NewKey == 0 to unregister.
//   - A key already bound to another port is never accepted.
func (m *Machine) Register(req Request) (*Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if req.Source == "" {
		return nil, prError(OpRegister, CodeInvalidRequest, ErrInvalidRequest)
	}
	reg, known := m.registrations[req.Source]
	switch {
	case !known:
		// A new port cannot authenticate with an existing key and cannot
		// register the zero key.
		if req.Key != 0 {
			return nil, prError(OpRegister, CodeKeyMismatch, ErrKeyMismatch)
		}
		if req.NewKey == 0 {
			return nil, prError(OpRegister, CodeInvalidRequest, ErrInvalidRequest)
		}
		if owner, taken := m.keyOwners[req.NewKey]; taken && owner != req.Source {
			return nil, prError(OpRegister, CodeKeyInUse, ErrKeyInUse)
		}
		m.registrations[req.Source] = &registration{key: req.NewKey, aptpl: req.APTPL}
		m.keyOwners[req.NewKey] = req.Source
		m.bumpGeneration()
	case req.NewKey == 0:
		// Unregister: the presented key must match the port's own binding.
		if req.Key != reg.key {
			return nil, prError(OpRegister, CodeKeyMismatch, ErrKeyMismatch)
		}
		m.removeRegistration(req.Source, reg)
		m.bumpGeneration()
	default:
		// Key rotation on an already registered port.
		if req.Key != reg.key {
			return nil, prError(OpRegister, CodeKeyMismatch, ErrKeyMismatch)
		}
		if owner, taken := m.keyOwners[req.NewKey]; taken && owner != req.Source {
			return nil, prError(OpRegister, CodeKeyInUse, ErrKeyInUse)
		}
		delete(m.keyOwners, reg.key)
		reg.key = req.NewKey
		reg.aptpl = req.APTPL
		m.keyOwners[req.NewKey] = req.Source
		m.bumpGeneration()
	}
	return m.responseLocked(OpRegister), nil
}

// Reserve creates the WERO reservation on behalf of a registered port. A
// reservation held by another registrant is a reservation conflict.
func (m *Machine) Reserve(req Request) (*Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.authenticate(OpReserve, req); err != nil {
		return nil, err
	}
	if m.reservation != nil && m.reservation.Holder != req.Source {
		return nil, prError(OpReserve, CodeReservationConflict, ErrReservationConflict)
	}
	if m.reservation == nil {
		m.reservation = &ReservationInfo{Holder: req.Source, Type: OpReserve}
		m.bumpGeneration()
	}
	return m.responseLocked(OpReserve), nil
}

// Release drops the reservation when issued by its holder. Releasing while no
// reservation exists is not a state change and does not move the generation.
func (m *Machine) Release(req Request) (*Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.authenticate(OpRelease, req); err != nil {
		return nil, err
	}
	if m.reservation != nil && m.reservation.Holder == req.Source {
		m.reservation = nil
		m.bumpGeneration()
	}
	return m.responseLocked(OpRelease), nil
}

// Preempt lets a registered, key-authenticated port remove another
// registrant's registration, identified by PreemptKey. If the preempted port
// held the reservation, ownership passes to the preemptor. Preempting one's
// own registration is invalid.
func (m *Machine) Preempt(req Request) (*Response, error) {
	return m.preempt(req, false)
}

// PreemptAndAbort is Preempt plus task abort for the preempted registrant; the
// successful response carries Aborted == true.
func (m *Machine) PreemptAndAbort(req Request) (*Response, error) {
	return m.preempt(req, true)
}

func (m *Machine) preempt(req Request, abort bool) (*Response, error) {
	op := OpPreempt
	if abort {
		op = OpPreemptAndAbort
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.authenticate(op, req); err != nil {
		return nil, err
	}
	if req.PreemptKey == 0 {
		return nil, prError(op, CodeInvalidRequest, ErrInvalidRequest)
	}
	victim, ok := m.keyOwners[req.PreemptKey]
	if !ok {
		return nil, prError(op, CodeInvalidRequest, ErrInvalidRequest)
	}
	if victim == req.Source {
		return nil, prError(op, CodeInvalidRequest, ErrInvalidRequest)
	}
	victimReg := m.registrations[victim]
	holderPreempted := m.reservation != nil && m.reservation.Holder == victim
	m.removeRegistration(victim, victimReg)
	if holderPreempted {
		m.reservation = &ReservationInfo{Holder: req.Source, Type: OpReserve}
	}
	m.bumpGeneration()
	resp := m.responseLocked(op)
	resp.Aborted = abort
	return resp, nil
}

// Clear removes every registration and the reservation. Only ports listed in
// the management policy may issue it; a CLEAR from any other port is rejected
// without touching state, so a compromised node cannot wipe the cluster's
// storage isolation.
func (m *Machine) Clear(req Request) (*Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.policy.ClearPorts[req.Source] {
		return nil, prError(OpClear, CodeClearNotAuthorized, ErrClearNotAuthorized)
	}
	if len(m.registrations) == 0 && m.reservation == nil {
		return m.responseLocked(OpClear), nil
	}
	m.registrations = make(map[PortID]*registration)
	m.keyOwners = make(map[Key]PortID)
	m.reservation = nil
	m.bumpGeneration()
	return m.responseLocked(OpClear), nil
}

// ReadKeys returns the keys of every registrant along with the generation.
func (m *Machine) ReadKeys(req Request) (*Response, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	resp := m.responseLocked(OpReadKeys)
	keys := make([]Key, 0, len(m.registrations))
	for _, reg := range m.registrations {
		keys = append(keys, reg.key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	resp.Keys = keys
	return resp, nil
}

// ReadReservation returns the current reservation (nil when unreserved) and
// the generation.
func (m *Machine) ReadReservation(req Request) (*Response, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.responseLocked(OpReadReservation), nil
}

// PowerLoss simulates a target power cycle and re-applies the APTPL policy.
// Registrations whose APTPL bit is set are retained with their key binding;
// all other registrations vanish. The reservation survives only when its
// holder still has an APTPL registration; otherwise it is dropped and the
// generation resets because no persistent state remains.
func (m *Machine) PowerLoss() {
	m.mu.Lock()
	defer m.mu.Unlock()

	survivors := make(map[PortID]*registration)
	survivingKeys := make(map[Key]PortID)
	for port, reg := range m.registrations {
		if reg.aptpl {
			survivors[port] = reg
			survivingKeys[reg.key] = port
		}
	}
	m.registrations = survivors
	m.keyOwners = survivingKeys
	if m.reservation != nil {
		if _, ok := survivors[m.reservation.Holder]; !ok {
			m.reservation = nil
		}
	}
	if len(survivors) == 0 {
		m.generation = 0
		m.reservation = nil
	}
}

// Dispatch is the single entry point used by the persistent-reservation
// request dispatcher. It routes an authenticated Request to the matching
// service action. Read-only actions take the read lock and never change the
// generation.
func (m *Machine) Dispatch(req Request) (*Response, error) {
	switch req.Op {
	case OpRegister:
		return m.Register(req)
	case OpReserve:
		return m.Reserve(req)
	case OpRelease:
		return m.Release(req)
	case OpPreempt:
		return m.Preempt(req)
	case OpPreemptAndAbort:
		return m.PreemptAndAbort(req)
	case OpClear:
		return m.Clear(req)
	case OpReadKeys:
		return m.ReadKeys(req)
	case OpReadReservation:
		return m.ReadReservation(req)
	default:
		return nil, prError(req.Op, CodeInvalidRequest, ErrInvalidRequest)
	}
}

// authenticate verifies that the request comes from a registered port and
// that the presented key matches the key bound to that port. Every action that
// acts on a reservation or another registrant goes through this check.
func (m *Machine) authenticate(op Opcode, req Request) (*registration, error) {
	reg, ok := m.registrations[req.Source]
	if !ok {
		return nil, prError(op, CodeNotRegistered, ErrNotRegistered)
	}
	if req.Key != reg.key {
		return nil, prError(op, CodeKeyMismatch, ErrKeyMismatch)
	}
	return reg, nil
}

// removeRegistration deletes a port's registration and its reverse key index.
// If the port held the reservation, the reservation is released because a WERO
// reservation without a holder registration is invalid.
func (m *Machine) removeRegistration(port PortID, reg *registration) {
	delete(m.registrations, port)
	delete(m.keyOwners, reg.key)
	if m.reservation != nil && m.reservation.Holder == port {
		m.reservation = nil
	}
}

func (m *Machine) bumpGeneration() {
	m.generation++
}

// responseLocked builds a response snapshot. Callers must hold at least the
// read lock.
func (m *Machine) responseLocked(op Opcode) *Response {
	resp := &Response{
		Op:         op,
		Generation: m.generation,
	}
	if m.reservation != nil {
		info := *m.reservation
		resp.Reservation = &info
	}
	return resp
}
