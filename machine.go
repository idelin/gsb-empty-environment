package netsec

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

// MAC is a 48-bit Ethernet hardware address. It is a value type so that
// bindings are comparable and safe to copy.
type MAC [6]byte

// ParseMAC parses an IEEE 802 MAC-48 address in the common colon or hyphen
// notation (for example "00:11:22:33:44:55").
func ParseMAC(s string) (MAC, error) {
	var mac MAC
	hw, err := net.ParseMAC(s)
	if err != nil {
		return mac, fmt.Errorf("netsec: parse MAC %q: %w", s, err)
	}
	if len(hw) != 6 {
		return mac, fmt.Errorf("netsec: %q is not a 48-bit MAC address", s)
	}
	copy(mac[:], hw)
	return mac, nil
}

// String renders the address in colon separated hexadecimal notation.
func (m MAC) String() string {
	const hexdigits = "0123456789abcdef"
	buf := make([]byte, 0, 17)
	for i, b := range m {
		if i > 0 {
			buf = append(buf, ':')
		}
		buf = append(buf, hexdigits[b>>4], hexdigits[b&0x0f])
	}
	return string(buf)
}

// IsZero reports whether the address is the all-zero MAC.
func (m MAC) IsZero() bool {
	for _, b := range m {
		if b != 0 {
			return false
		}
	}
	return true
}

// Ticket is a migration authorization minted by the trusted control plane.
// Generation numbers for a given IP must be strictly increasing. The data
// plane is never allowed to create tickets; it can only present them.
type Ticket struct {
	IP         netip.Addr
	NewMAC     MAC
	Generation uint64
}

// State is the lifecycle state of one IP neighbor entry.
type State int

const (
	// StateEmpty means no binding exists for the IP.
	StateEmpty State = iota
	// StateBound means a stable, ticket-backed binding exists.
	StateBound
	// StateMigrating means a newer ticket is pending a data plane ARP
	// confirmation; traffic still follows the previous stable binding.
	StateMigrating
	// StateQuarantined means an unauthorized takeover was detected. The
	// entry is frozen, traffic is dropped and evidence is retained until
	// the control plane releases it or grants a newer ticket.
	StateQuarantined
)

// String returns the human readable state name.
func (s State) String() string {
	switch s {
	case StateEmpty:
		return "empty"
	case StateBound:
		return "bound"
	case StateMigrating:
		return "migrating"
	case StateQuarantined:
		return "quarantined"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// EventKind enumerates the audit events emitted by the state machine. Every
// state transition and every rejected data plane attempt is recorded so that
// quarantine incidents can be reconstructed after the fact.
type EventKind int

const (
	EventTicketGranted EventKind = iota
	EventMigrationConfirmed
	EventArpRefresh
	EventArpDenied
	EventArpConflict
	EventProbeTimeout
	EventTicketRevoked
	EventTicketReleased
	EventTicketRejected
	EventQuarantined
)

// String returns the human readable event name.
func (k EventKind) String() string {
	switch k {
	case EventTicketGranted:
		return "ticket_granted"
	case EventMigrationConfirmed:
		return "migration_confirmed"
	case EventArpRefresh:
		return "arp_refresh"
	case EventArpDenied:
		return "arp_denied"
	case EventArpConflict:
		return "arp_conflict"
	case EventProbeTimeout:
		return "probe_timeout"
	case EventTicketRevoked:
		return "ticket_revoked"
	case EventTicketReleased:
		return "ticket_released"
	case EventTicketRejected:
		return "ticket_rejected"
	case EventQuarantined:
		return "quarantined"
	default:
		return fmt.Sprintf("unknown(%d)", int(k))
	}
}

// ForwardAction tells the switching host what to do with a data frame.
type ForwardAction int

const (
	// ForwardDeliver sends the frame to the port identified by MAC.
	ForwardDeliver ForwardAction = iota
	// ForwardDrop discards the frame because no safe destination exists.
	ForwardDrop
)

// String returns the human readable action name.
func (a ForwardAction) String() string {
	switch a {
	case ForwardDeliver:
		return "deliver"
	case ForwardDrop:
		return "drop"
	default:
		return fmt.Sprintf("unknown(%d)", int(a))
	}
}

// Event is one immutable audit record.
type Event struct {
	Kind       EventKind
	At         time.Time
	IP         netip.Addr
	MAC        MAC
	Generation uint64
	Detail     string
}

// ForwardDecision is the result of looking up an IP for frame forwarding.
type ForwardDecision struct {
	Action ForwardAction
	// MAC is the destination MAC when Action is ForwardDeliver, zero otherwise.
	MAC MAC
}

// EntrySnapshot is an immutable view of one neighbor entry, including the
// retained evidence trail.
type EntrySnapshot struct {
	IP         netip.Addr
	State      State
	MAC        MAC
	Generation uint64
	Pending    *Ticket
	MaxGen     uint64
	Events     []Event
}

// Errors returned by the control plane and data plane entry points.
var (
	// ErrInvalidArgument is returned for malformed IPs, tickets or MACs.
	ErrInvalidArgument = errors.New("netsec: invalid argument")
	// ErrStaleTicket is returned when a ticket generation is not newer
	// than the highest generation already observed for the IP.
	ErrStaleTicket = errors.New("netsec: stale ticket generation")
	// ErrUnknownTicket is returned when revoking a generation that the
	// state machine has no record of.
	ErrUnknownTicket = errors.New("netsec: unknown ticket generation")
	// ErrInvalidState is returned when an event does not apply to the
	// entry's current state.
	ErrInvalidState = errors.New("netsec: operation invalid in current state")
	// ErrArpDenied is returned for every data plane ARP claim that the
	// state machine refuses.
	ErrArpDenied = errors.New("netsec: ARP claim denied")
)

// Machine is the neighbor state machine for a virtual switch instance. It is
// safe for concurrent use by the control plane and data plane goroutines of
// the Go switching host.
type Machine struct {
	mu      sync.RWMutex
	entries map[netip.Addr]*entry
	now     func() time.Time
}

// Option configures a Machine at construction time.
type Option func(*Machine)

// WithClock injects a clock, primarily for deterministic tests.
func WithClock(now func() time.Time) Option {
	return func(m *Machine) {
		if now != nil {
			m.now = now
		}
	}
}

// NewMachine creates an empty neighbor state machine.
func NewMachine(opts ...Option) *Machine {
	m := &Machine{
		entries: make(map[netip.Addr]*entry),
		now:     time.Now,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

type entry struct {
	ip    netip.Addr
	state State

	// Stable binding (valid in StateBound and StateMigrating). During
	// migration this remains the old, still-forwarded binding.
	mac MAC
	gen uint64

	// Pending migration ticket, non-nil only in StateMigrating.
	pending *Ticket

	// Highest ticket generation ever accepted for this IP. It is retained
	// across timeouts, revocations and quarantine so that old tickets can
	// never be replayed.
	maxGen uint64

	// Complete evidence trail. Entries are never deleted by data plane
	// events; even released entries keep their history.
	events []Event
}

func (m *Machine) log(e *entry, kind EventKind, mac MAC, gen uint64, detail string) {
	e.events = append(e.events, Event{
		Kind:       kind,
		At:         m.now(),
		IP:         e.ip,
		MAC:        mac,
		Generation: gen,
		Detail:     detail,
	})
}

// quarantine freezes the entry and records both a conflict record and the
// quarantine transition as evidence. The caller holds the write lock.
func (m *Machine) quarantine(e *entry, offendingMAC MAC, gen uint64, detail string) {
	e.state = StateQuarantined
	e.pending = nil
	m.log(e, EventArpConflict, offendingMAC, gen, detail)
	m.log(e, EventQuarantined, offendingMAC, gen, detail)
}

// Grant accepts a migration ticket from the trusted control plane. Tickets
// must carry a generation strictly greater than every generation previously
// observed for the IP; stale or replayed tickets are rejected and recorded as
// evidence without changing the binding.
func (m *Machine) Grant(t Ticket) error {
	if !t.IP.IsValid() || t.NewMAC.IsZero() || t.Generation == 0 {
		return ErrInvalidArgument
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	e := m.entries[t.IP]
	if e == nil {
		e = &entry{ip: t.IP, state: StateEmpty}
		m.entries[t.IP] = e
	}

	if t.Generation <= e.maxGen {
		m.log(e, EventTicketRejected, t.NewMAC, t.Generation,
			"ticket generation is not newer than observed generation "+strconv.FormatUint(e.maxGen, 10))
		return fmt.Errorf("%w: gen %d <= max gen %d for %s",
			ErrStaleTicket, t.Generation, e.maxGen, t.IP)
	}

	switch e.state {
	case StateEmpty, StateQuarantined:
		// First binding, or recovery from isolation: there is no stable
		// binding to fall back to, so the ticket still needs ARP proof.
		e.state = StateMigrating
		e.pending = &t
	case StateBound:
		if e.mac == t.NewMAC {
			// Re-affirmation of the same binding: no migration window is
			// needed, but the newer generation is still recorded.
			e.gen = t.Generation
			e.maxGen = t.Generation
			m.log(e, EventTicketGranted, t.NewMAC, t.Generation, "binding re-affirmed")
			return nil
		}
		e.state = StateMigrating
		e.pending = &t
	case StateMigrating:
		// A newer ticket supersedes a ticket that has not been confirmed.
		e.pending = &t
	default:
		return fmt.Errorf("%w: %s", ErrInvalidState, e.state)
	}

	e.maxGen = t.Generation
	m.log(e, EventTicketGranted, t.NewMAC, t.Generation, "")
	return nil
}

// HandleArpClaim processes a data plane ARP announcement or request claiming
// an IP/MAC association. Only the MAC authorized by the currently pending
// ticket can confirm a migration; ARP from the incumbent MAC merely refreshes
// a stable binding. Any other MAC is an unauthorized takeover: the claim is
// refused and the entry is quarantined with evidence retained.
func (m *Machine) HandleArpClaim(ip netip.Addr, mac MAC) error {
	if !ip.IsValid() || mac.IsZero() {
		return ErrInvalidArgument
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	e := m.entries[ip]
	if e == nil {
		e = &entry{ip: ip, state: StateEmpty}
		m.entries[ip] = e
	}

	switch e.state {
	case StateEmpty:
		m.quarantine(e, mac, 0, "ARP claim without any ticket")
		return ErrArpDenied

	case StateBound:
		if mac == e.mac {
			m.log(e, EventArpRefresh, mac, e.gen, "")
			return nil
		}
		m.quarantine(e, mac, e.gen, "unauthorized ARP claim for bound IP")
		return ErrArpDenied

	case StateMigrating:
		if e.pending != nil && mac == e.pending.NewMAC {
			oldMAC := e.mac
			e.mac = e.pending.NewMAC
			e.gen = e.pending.Generation
			e.pending = nil
			e.state = StateBound
			detail := ""
			if !oldMAC.IsZero() {
				detail = "migrated from " + oldMAC.String()
			}
			m.log(e, EventMigrationConfirmed, e.mac, e.gen, detail)
			return nil
		}
		// Any other MAC cannot confirm the pending ticket. An old MAC
		// re-announcement is a late confirmation of a superseded binding
		// and is refused rather than allowed to roll the binding back.
		detail := "ARP claim does not match pending ticket"
		if mac == e.mac {
			detail = "late ARP for superseded binding rejected"
		}
		m.quarantine(e, mac, e.maxGen, detail)
		return ErrArpDenied

	case StateQuarantined:
		m.log(e, EventArpDenied, mac, e.maxGen, "ARP claim while quarantined")
		return ErrArpDenied

	default:
		return fmt.Errorf("%w: %s", ErrInvalidState, e.state)
	}
}

// ProbeTimeout marks a pending migration ticket as unconfirmed. A migration
// that never received the ticket-authorized ARP confirmation fails: if a
// previous stable binding exists, traffic reverts to it; otherwise the entry
// returns to empty. The failed generation stays consumed so the ticket cannot
// be replayed.
func (m *Machine) ProbeTimeout(ip netip.Addr) error {
	if !ip.IsValid() {
		return ErrInvalidArgument
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	e := m.entries[ip]
	if e == nil || e.state != StateMigrating || e.pending == nil {
		return fmt.Errorf("%w: no pending migration for %s", ErrInvalidState, ip)
	}

	failedGen := e.pending.Generation
	failedMAC := e.pending.NewMAC
	e.pending = nil
	if e.mac.IsZero() {
		e.state = StateEmpty
	} else {
		e.state = StateBound
	}
	m.log(e, EventProbeTimeout, failedMAC, failedGen, "migration ticket expired unconfirmed")
	return nil
}

// Revoke withdraws a ticket generation previously granted by the control
// plane. Revoking the active binding clears it; revoking a pending migration
// falls back to the previous stable binding. Revoking a generation that is
// neither active nor pending, or one never seen, is an error and changes
// nothing. Quarantined entries must be released, not revoked.
func (m *Machine) Revoke(ip netip.Addr, generation uint64) error {
	if !ip.IsValid() || generation == 0 {
		return ErrInvalidArgument
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	e := m.entries[ip]
	if e == nil || generation > e.maxGen {
		return fmt.Errorf("%w: gen %d for %s", ErrUnknownTicket, generation, ip)
	}

	switch e.state {
	case StateBound:
		if generation != e.gen {
			return fmt.Errorf("%w: gen %d is not the active gen %d for %s",
				ErrUnknownTicket, generation, e.gen, ip)
		}
		revokedMAC := e.mac
		e.mac = MAC{}
		e.gen = 0
		e.state = StateEmpty
		m.log(e, EventTicketRevoked, revokedMAC, generation, "active binding revoked")
		return nil

	case StateMigrating:
		if e.pending != nil && generation == e.pending.Generation {
			pendingMAC := e.pending.NewMAC
			e.pending = nil
			if e.mac.IsZero() {
				e.state = StateEmpty
			} else {
				e.state = StateBound
			}
			m.log(e, EventTicketRevoked, pendingMAC, generation, "pending migration revoked")
			return nil
		}
		if e.gen != 0 && generation == e.gen {
			// The control plane withdrew the incumbent underneath an
			// in-flight migration: the fallback disappears with it.
			oldMAC := e.mac
			e.mac = MAC{}
			e.gen = 0
			m.log(e, EventTicketRevoked, oldMAC, generation, "incumbent binding revoked during migration")
			return nil
		}
		return fmt.Errorf("%w: gen %d is neither active nor pending for %s",
			ErrUnknownTicket, generation, ip)

	case StateQuarantined:
		return fmt.Errorf("%w: %s is quarantined; release it first", ErrInvalidState, ip)

	case StateEmpty:
		return fmt.Errorf("%w: no active ticket for %s", ErrUnknownTicket, ip)

	default:
		return fmt.Errorf("%w: %s", ErrInvalidState, e.state)
	}
}

// Release clears a quarantined entry so that the control plane can re-onboard
// the IP with a fresh, higher-generation ticket. The full evidence trail is
// retained. Releasing an entry that is not quarantined is an error.
func (m *Machine) Release(ip netip.Addr) error {
	if !ip.IsValid() {
		return ErrInvalidArgument
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	e := m.entries[ip]
	if e == nil || e.state != StateQuarantined {
		return fmt.Errorf("%w: %s is not quarantined", ErrInvalidState, ip)
	}

	offending := e.mac
	m.log(e, EventTicketReleased, offending, e.maxGen, "quarantine released by control plane")
	e.state = StateEmpty
	e.mac = MAC{}
	e.gen = 0
	e.pending = nil
	return nil
}

// Lookup decides where a data frame destined for ip must go. During a
// migration frames keep following the incumbent MAC until the new MAC has
// confirmed its ticket; quarantined and unbound IPs drop the frame so that
// traffic can never reach an unauthorized MAC.
func (m *Machine) Lookup(ip netip.Addr) ForwardDecision {
	m.mu.RLock()
	defer m.mu.RUnlock()

	e := m.entries[ip]
	if e == nil || e.state == StateEmpty || e.state == StateQuarantined || e.mac.IsZero() {
		return ForwardDecision{Action: ForwardDrop}
	}
	return ForwardDecision{Action: ForwardDeliver, MAC: e.mac}
}

// Snapshot returns an immutable view of the entry for ip, or nil if the IP
// has never been observed. The evidence slice is a copy and may be inspected
// by the control plane or by tests without holding any lock.
func (m *Machine) Snapshot(ip netip.Addr) *EntrySnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	e := m.entries[ip]
	if e == nil {
		return nil
	}

	snap := &EntrySnapshot{
		IP:         e.ip,
		State:      e.state,
		MAC:        e.mac,
		Generation: e.gen,
		MaxGen:     e.maxGen,
		Events:     append([]Event(nil), e.events...),
	}
	if e.pending != nil {
		pending := *e.pending
		snap.Pending = &pending
	}
	return snap
}

// Evidence returns the retained audit trail for ip. It is nil for IPs that
// have never been observed.
func (m *Machine) Evidence(ip netip.Addr) []Event {
	snap := m.Snapshot(ip)
	if snap == nil {
		return nil
	}
	return snap.Events
}
