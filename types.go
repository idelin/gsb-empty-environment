package netsec

import (
	"errors"
	"net/netip"
	"time"
)

// State is the neighbor-unreachability state of a single IP binding.
type State uint8

const (
	// StateEmpty means no neighbor and no ticket exist for the IP.
	StateEmpty State = iota
	// StateIncomplete means ARP resolution was attempted but never confirmed.
	StateIncomplete
	// StateReachable means a ticket-authorized MAC has been confirmed on a port.
	StateReachable
	// StateStale means the reachability timer expired; the cached binding is
	// retained but must be re-probed before it is trusted again.
	StateStale
	// StateProbe means ARP probes are in flight waiting for a confirmation.
	StateProbe
	// StateQuarantined means an unauthorized takeover was detected; data frames
	// for the IP are dropped until the control plane intervenes.
	StateQuarantined
)

// String returns a stable human-readable name for the state.
func (s State) String() string {
	switch s {
	case StateEmpty:
		return "empty"
	case StateIncomplete:
		return "incomplete"
	case StateReachable:
		return "reachable"
	case StateStale:
		return "stale"
	case StateProbe:
		return "probe"
	case StateQuarantined:
		return "quarantined"
	default:
		return "unknown"
	}
}

// PortID identifies a virtual switch port.
type PortID uint16

// MAC is an Ethernet hardware address stored as canonical lowercase hex
// with colons, e.g. "02:00:00:00:00:01".
type MAC string

// Valid reports whether m parses as a 6-byte Ethernet address.
func (m MAC) Valid() bool {
	_, err := parseMAC(string(m))
	return err == nil
}

// Reason explains why a data-plane ARP observation was rejected.
type Reason uint8

const (
	// ReasonNone marks an accepted observation.
	ReasonNone Reason = iota
	// ReasonNoTicket: no active control-plane ticket covers the IP.
	ReasonNoTicket
	// ReasonMACNotAllowed: the sender MAC is not the ticket's authorized MAC.
	ReasonMACNotAllowed
	// ReasonQuarantined: the neighbor is isolated and cannot be confirmed.
	ReasonQuarantined
)

// String returns a stable human-readable name for the reason.
func (r Reason) String() string {
	switch r {
	case ReasonNone:
		return "none"
	case ReasonNoTicket:
		return "no_ticket"
	case ReasonMACNotAllowed:
		return "mac_not_allowed"
	case ReasonQuarantined:
		return "quarantined"
	default:
		return "unknown"
	}
}

// Ticket is a control-plane migration credential. It authorizes exactly one
// MAC to own an IP. Each subsequent ticket for the same IP must carry a
// strictly larger Generation; equal or smaller generations are rejected as
// stale and can never overwrite a newer binding.
type Ticket struct {
	IP         netip.Addr
	MAC        MAC
	Generation uint64
	// TTL is the remaining validity from the moment the ticket is granted.
	// A zero TTL means the ticket never expires on its own.
	TTL time.Duration
}

// EvidenceKind classifies a retained conflict artifact.
type EvidenceKind uint8

const (
	// EvidenceUnauthorizedARP is an ARP frame from a MAC that no active
	// ticket authorizes (takeover attempt, including a stale ticket holder).
	EvidenceUnauthorizedARP EvidenceKind = iota
	// EvidenceMACConflict is two different MACs claiming the same IP while a
	// binding already exists.
	EvidenceMACConflict
	// EvidenceLateConfirm is an ARP confirmation for an older generation
	// arriving after a newer binding was installed.
	EvidenceLateConfirm
)

// String returns a stable human-readable name for the evidence kind.
func (k EvidenceKind) String() string {
	switch k {
	case EvidenceUnauthorizedARP:
		return "unauthorized_arp"
	case EvidenceMACConflict:
		return "mac_conflict"
	case EvidenceLateConfirm:
		return "late_confirm"
	default:
		return "unknown"
	}
}

// Evidence is an immutable forensic record retained when a neighbor is
// quarantined. Evidence survives ticket revocation and re-grant until it is
// explicitly drained or ages out of the bounded per-IP ring.
type Evidence struct {
	Seq          uint64
	At           time.Time
	IP           netip.Addr
	Kind         EvidenceKind
	ObservedMAC  MAC
	ObservedPort PortID
	AllowedMAC   MAC
	// TicketGen is the generation of the ticket that was active (zero if none).
	TicketGen uint64
	// BindingGen is the generation behind the current binding (zero if none).
	BindingGen uint64
}

// EventType enumerates the state transitions and security decisions surfaced
// to the switch host for auditing.
type EventType uint8

const (
	EventTicketGranted EventType = iota
	EventTicketRejected
	EventTicketRevoked
	EventTicketExpired
	EventBindingInstalled
	EventBindingConfirmed
	EventARPRejected
	EventQuarantined
	EventProbeStarted
	EventProbeTimeout
	EventResolutionFailed
	EventForwardingChanged
)

// String returns a stable human-readable name for the event type.
func (t EventType) String() string {
	switch t {
	case EventTicketGranted:
		return "ticket_granted"
	case EventTicketRejected:
		return "ticket_rejected"
	case EventTicketRevoked:
		return "ticket_revoked"
	case EventTicketExpired:
		return "ticket_expired"
	case EventBindingInstalled:
		return "binding_installed"
	case EventBindingConfirmed:
		return "binding_confirmed"
	case EventARPRejected:
		return "arp_rejected"
	case EventQuarantined:
		return "quarantined"
	case EventProbeStarted:
		return "probe_started"
	case EventProbeTimeout:
		return "probe_timeout"
	case EventResolutionFailed:
		return "resolution_failed"
	case EventForwardingChanged:
		return "forwarding_changed"
	default:
		return "unknown"
	}
}

// Event is an auditable observation emitted by the state machine.
type Event struct {
	Seq    uint64
	At     time.Time
	Type   EventType
	IP     netip.Addr
	MAC    MAC
	Port   PortID
	Gen    uint64
	State  State
	Reason Reason
}

// FrameVerdict tells the data plane what to do with a frame addressed to an IP.
type FrameVerdict uint8

const (
	// VerdictForward delivers the frame out of one specific port.
	VerdictForward FrameVerdict = iota
	// VerdictFlood sends the frame to every forwarding port except the ingress.
	VerdictFlood
	// VerdictDrop discards the frame (quarantined destination).
	VerdictDrop
)

// String returns a stable human-readable name for the verdict.
func (v FrameVerdict) String() string {
	switch v {
	case VerdictForward:
		return "forward"
	case VerdictFlood:
		return "flood"
	case VerdictDrop:
		return "drop"
	default:
		return "unknown"
	}
}

var (
	// ErrInvalidMAC is returned when a MAC address cannot be parsed.
	ErrInvalidMAC = errors.New("netsec: invalid mac address")
	// ErrInvalidIP is returned when an IP address is unset.
	ErrInvalidIP = errors.New("netsec: invalid ip address")
	// ErrStaleTicket is returned when a ticket generation is not strictly
	// greater than the current generation for the same IP.
	ErrStaleTicket = errors.New("netsec: stale ticket generation")
	// ErrUnknownNeighbor is returned when an operation targets an IP the
	// switch has never learned about.
	ErrUnknownNeighbor = errors.New("netsec: unknown neighbor")
	// ErrNoTicket is returned when resolution/revalidation is requested
	// without an active control-plane ticket.
	ErrNoTicket = errors.New("netsec: no active ticket")
	// ErrAlreadyBound is returned when resolution is requested for an IP
	// that already has a confirmed binding.
	ErrAlreadyBound = errors.New("netsec: binding already installed")
	// ErrNotStale is returned when revalidation is requested for a neighbor
	// that is not in the stale state.
	ErrNotStale = errors.New("netsec: neighbor is not stale")
	// ErrQuarantined is returned when a data-plane operation is refused
	// because the neighbor is isolated.
	ErrQuarantined = errors.New("netsec: neighbor quarantined")
)
