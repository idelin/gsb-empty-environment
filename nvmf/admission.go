// Package nvmf implements Connect command admission control for an NVMe over
// Fabrics target.
//
// The policy enforced here is deliberately strict:
//
//   - Host NQNs must be valid UTF-8, at most 223 bytes, and match one of the
//     two specification-defined NQN forms. Anything else is rejected.
//   - Subsystem allowlists compare host NQNs byte for byte. No Unicode
//     normalization and no case folding is performed, so "Nqn..." cannot match
//     an allowlist entry of "nqn...".
//   - A host NQN is bound to the HostID presented on its first authenticated
//     connection. While that binding is active, the same NQN arriving with a
//     different HostID is rejected.
//   - Discovery log pages only contain subsystems that the requesting host is
//     allowed to access.
//   - A controller ID whose admin queue is already established cannot be reused
//     by another host's IO queue Connect.
package nvmf

import (
	"errors"
	"sort"
	"sync"
)

// AdminQueueID is the fixed queue ID of an NVMe-oF admin queue.
const AdminQueueID = 0

var (
	// ErrEmptyNQN is returned when an empty host NQN is presented.
	ErrEmptyNQN = errors.New("nvmf: empty host NQN")
	// ErrNQNTooLong is returned when a host NQN exceeds 223 bytes.
	ErrNQNTooLong = errors.New("nvmf: host NQN exceeds 223 bytes")
	// ErrNQNNotUTF8 is returned when a host NQN is not valid UTF-8.
	ErrNQNNotUTF8 = errors.New("nvmf: host NQN is not valid UTF-8")
	// ErrInvalidNQN is returned when a host NQN matches neither allowed form.
	ErrInvalidNQN = errors.New("nvmf: malformed host NQN")
	// ErrMalformedUUID is returned when the UUID of a uuid-named NQN is bad.
	ErrMalformedUUID = errors.New("nvmf: malformed UUID in host NQN")

	// ErrUnknownSubsystem is returned when the target subsystem is not known.
	ErrUnknownSubsystem = errors.New("nvmf: unknown subsystem NQN")
	// ErrHostNotAllowed is returned when the host NQN is not in the subsystem
	// allowlist. The comparison is an exact byte comparison.
	ErrHostNotAllowed = errors.New("nvmf: host NQN not in subsystem allowlist")
	// ErrHostIDMismatch is returned when an active NQN is reused with a
	// different HostID.
	ErrHostIDMismatch = errors.New("nvmf: host NQN already bound to another HostID")
	// ErrEmptyHostID is returned when the Connect carries the all-zero HostID.
	ErrEmptyHostID = errors.New("nvmf: empty HostID in Connect")
	// ErrControllerInUse is returned when a controller ID established by
	// another host is reused by an IO queue Connect.
	ErrControllerInUse = errors.New("nvmf: controller ID already owned by another host")
	// ErrNoAdminQueue is returned when an IO queue Connect references a
	// controller ID with no established admin queue.
	ErrNoAdminQueue = errors.New("nvmf: IO queue Connect for unknown controller ID")
)

// HostID is the 128-bit Host Identifier carried in a Connect command.
type HostID [16]byte

// ConnectRequest describes the fields of a Connect command relevant to
// admission control.
type ConnectRequest struct {
	// HostNQN is the NQN the connecting host claims.
	HostNQN string
	// HostID is the 128-bit host identifier presented alongside the NQN.
	HostID HostID
	// SubsystemNQN is the target subsystem the host wants to connect to.
	SubsystemNQN string
	// ControllerID is the requested controller ID. IO queue Connects reuse the
	// controller ID allocated by the admin queue Connect.
	ControllerID uint16
	// QueueID is AdminQueueID (0) for admin queue Connects, positive for IO
	// queue Connects.
	QueueID uint16
}

// Subsystem models an NVMe-oF subsystem with its explicit host allowlist.
type Subsystem struct {
	// NQN is the subsystem NQN.
	NQN string

	allowedHosts map[string]struct{}
}

// NewSubsystem creates a subsystem identified by nqn and seeds its allowlist
// with the given host NQNs. Host NQNs are validated; malformed entries are
// rejected instead of silently stored, so an allowlist can never contain a
// value that ValidateHostNQN would refuse.
func NewSubsystem(nqn string, allowedHosts ...string) (*Subsystem, error) {
	if nqn == "" {
		return nil, ErrEmptyNQN
	}
	sub := &Subsystem{NQN: nqn, allowedHosts: make(map[string]struct{})}
	for _, host := range allowedHosts {
		if err := sub.AllowHost(host); err != nil {
			return nil, err
		}
	}
	return sub, nil
}

// AllowHost adds hostNQN to the subsystem allowlist after validating it.
func (s *Subsystem) AllowHost(hostNQN string) error {
	if err := ValidateHostNQN(hostNQN); err != nil {
		return err
	}
	s.allowedHosts[hostNQN] = struct{}{}
	return nil
}

// AllowedHosts returns the allowlisted host NQNs in sorted order.
func (s *Subsystem) AllowedHosts() []string {
	hosts := make([]string, 0, len(s.allowedHosts))
	for host := range s.allowedHosts {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts
}

// allows reports whether hostNQN is allowlisted. The map lookup is an exact
// byte comparison: no case folding, trimming or Unicode normalization occurs.
func (s *Subsystem) allows(hostNQN string) bool {
	_, ok := s.allowedHosts[hostNQN]
	return ok
}

// binding records the HostID a host NQN was first authenticated with and the
// number of live connections currently holding that binding.
type binding struct {
	hostID   HostID
	refCount int
}

// Authorizer makes and enforces Connect admission decisions for a target. It
// is safe for concurrent use.
type Authorizer struct {
	mu sync.Mutex

	subsystems map[string]*Subsystem

	// bindings keys are exact host NQN bytes.
	bindings map[string]*binding

	// controllerOwners maps subsystem NQN -> controller ID -> owning host NQN.
	controllerOwners map[string]map[uint16]string
}

// NewAuthorizer creates an Authorizer over the given subsystems.
func NewAuthorizer(subsystems ...*Subsystem) *Authorizer {
	a := &Authorizer{
		subsystems:       make(map[string]*Subsystem),
		bindings:         make(map[string]*binding),
		controllerOwners: make(map[string]map[uint16]string),
	}
	for _, sub := range subsystems {
		a.subsystems[sub.NQN] = sub
	}
	return a
}

// AddSubsystem registers a subsystem with the authorizer.
func (a *Authorizer) AddSubsystem(sub *Subsystem) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.subsystems[sub.NQN] = sub
}

// AdmitConnect decides whether a Connect command is allowed. On success the
// host NQN/HostID binding and, for admin queue Connects, the controller ID
// ownership are recorded. Every check runs before any state is mutated.
func (a *Authorizer) AdmitConnect(req ConnectRequest) error {
	if err := ValidateHostNQN(req.HostNQN); err != nil {
		return err
	}
	if req.HostID == (HostID{}) {
		return ErrEmptyHostID
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	sub, ok := a.subsystems[req.SubsystemNQN]
	if !ok {
		return ErrUnknownSubsystem
	}
	if !sub.allows(req.HostNQN) {
		return ErrHostNotAllowed
	}

	bound, ok := a.bindings[req.HostNQN]
	if ok && bound.refCount > 0 && bound.hostID != req.HostID {
		return ErrHostIDMismatch
	}

	if req.QueueID == AdminQueueID {
		if owner, exists := a.controllerOwners[req.SubsystemNQN][req.ControllerID]; exists && owner != req.HostNQN {
			return ErrControllerInUse
		}
	} else {
		owner, exists := a.controllerOwners[req.SubsystemNQN][req.ControllerID]
		if !exists {
			return ErrNoAdminQueue
		}
		if owner != req.HostNQN {
			return ErrControllerInUse
		}
	}

	if ok {
		bound.refCount++
	} else {
		a.bindings[req.HostNQN] = &binding{hostID: req.HostID, refCount: 1}
	}

	if req.QueueID == AdminQueueID {
		owners := a.controllerOwners[req.SubsystemNQN]
		if owners == nil {
			owners = make(map[uint16]string)
			a.controllerOwners[req.SubsystemNQN] = owners
		}
		owners[req.ControllerID] = req.HostNQN
	}
	return nil
}

// ReleaseConnection undoes the bookkeeping done by a successful AdmitConnect
// when the connection is torn down. The binding refcount is decremented and a
// HostID becomes reusable by another host once it reaches zero. Admin queue
// releases also free the controller ID for later allocation.
func (a *Authorizer) ReleaseConnection(req ConnectRequest) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if bound, ok := a.bindings[req.HostNQN]; ok {
		bound.refCount--
		if bound.refCount <= 0 {
			delete(a.bindings, req.HostNQN)
		}
	}

	if req.QueueID == AdminQueueID {
		if owners, ok := a.controllerOwners[req.SubsystemNQN]; ok {
			if owner, exists := owners[req.ControllerID]; exists && owner == req.HostNQN {
				delete(owners, req.ControllerID)
			}
		}
	}
}

// ReleaseController drops the admin queue ownership of a controller ID without
// touching NQN bindings. It is intended for target-side teardown paths that do
// not have the original Connect request at hand.
func (a *Authorizer) ReleaseController(subsystemNQN string, controllerID uint16) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if owners, ok := a.controllerOwners[subsystemNQN]; ok {
		delete(owners, controllerID)
	}
}

// DiscoveryEntry is one row of a discovery log page.
type DiscoveryEntry struct {
	SubsystemNQN string
}

// DiscoveryLog returns the discovery log entries a requesting host is allowed
// to see: precisely the registered subsystems whose allowlist contains the
// host NQN, compared byte for byte. The host NQN itself is validated first and
// malformed values yield no entries.
func (a *Authorizer) DiscoveryLog(hostNQN string) ([]DiscoveryEntry, error) {
	if err := ValidateHostNQN(hostNQN); err != nil {
		return nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	nqns := make([]string, 0, len(a.subsystems))
	for nqn := range a.subsystems {
		nqns = append(nqns, nqn)
	}
	sort.Strings(nqns)

	entries := make([]DiscoveryEntry, 0)
	for _, nqn := range nqns {
		if a.subsystems[nqn].allows(hostNQN) {
			entries = append(entries, DiscoveryEntry{SubsystemNQN: nqn})
		}
	}
	return entries, nil
}
