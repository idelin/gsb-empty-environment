package netsec

import (
	"fmt"
	"strings"
	"time"
)

// ASN and RouterID identify the remote BGP speaker.  They are deliberately
// plain aliases so callers can use ordinary literals.  A router ID is not
// globally unique: the pair (NeighborAS, RouterID) is the peer identity.
type ASN = uint32
type RouterID = string
type RouteID = string
type Prefix = string

// PolicyVersion and VRPVersion are monotonic host-side revision numbers.
type PolicyVersion = uint64
type VRPVersion = uint64

// Peer is the stable identity of a BGP neighbor.
type Peer struct {
	NeighborAS ASN      `json:"neighbor_as"`
	AS         ASN      `json:"-"`
	RouterID   RouterID `json:"router_id"`
}

func (p Peer) key() peerKey {
	return peerKey{neighborAS: p.effectiveAS(), routerID: p.RouterID}
}

func (p Peer) effectiveAS() ASN {
	if p.NeighborAS != 0 {
		return p.NeighborAS
	}
	return p.AS
}

func (p Peer) normalized() Peer {
	p.NeighborAS = p.effectiveAS()
	p.AS = p.NeighborAS
	p.RouterID = strings.TrimSpace(p.RouterID)
	return p
}

func (p Peer) String() string {
	return fmt.Sprintf("AS%d/%s", p.effectiveAS(), p.RouterID)
}

// SessionIdentity binds a transport incarnation to a peer and the import
// policy revision that was current when the session was established.  The
// transport generation must increase on every reconnect.
type SessionIdentity struct {
	NeighborAS          ASN           `json:"neighbor_as"`
	AS                  ASN           `json:"-"`
	RouterID            RouterID      `json:"router_id"`
	TransportGeneration uint64        `json:"transport_generation"`
	TransportGen        uint64        `json:"-"`
	TransportID         uint64        `json:"-"`
	PolicyVersion       PolicyVersion `json:"policy_version"`
	VRPVersion          VRPVersion    `json:"vrp_version"`
}

func (id SessionIdentity) generation() uint64 {
	if id.TransportGeneration != 0 {
		return id.TransportGeneration
	}
	if id.TransportGen != 0 {
		return id.TransportGen
	}
	return id.TransportID
}

func (id SessionIdentity) effectiveAS() ASN {
	if id.NeighborAS != 0 {
		return id.NeighborAS
	}
	return id.AS
}

func (id SessionIdentity) peer() Peer {
	return Peer{NeighborAS: id.effectiveAS(), RouterID: id.RouterID}
}

func (id SessionIdentity) normalized() SessionIdentity {
	id.RouterID = strings.TrimSpace(id.RouterID)
	id.NeighborAS = id.effectiveAS()
	id.AS = id.NeighborAS
	if id.TransportGeneration == 0 {
		id.TransportGeneration = id.TransportGen
	}
	if id.TransportGeneration == 0 {
		id.TransportGeneration = id.TransportID
	}
	id.TransportGen = id.TransportGeneration
	id.TransportID = id.TransportGeneration
	return id
}

// Capabilities is the subset of BGP capability negotiation relevant to this
// ledger.  A peer can only have its routes marked stale when it negotiated
// graceful restart on the session that is being torn down.
type Capabilities struct {
	GracefulRestart bool          `json:"graceful_restart"`
	Restart         bool          `json:"-"`
	Graceful        bool          `json:"-"`
	GR              bool          `json:"-"`
	RestartTime     time.Duration `json:"restart_time,omitempty"`
}

func (c Capabilities) graceful() bool {
	return c.GracefulRestart || c.Restart || c.Graceful || c.GR
}

// RestartCapability is a convenient alias used by simple hosts.
type RestartCapability = bool

// DisconnectReason explains why a transport went away.
type DisconnectReason int

const (
	DisconnectOther DisconnectReason = iota
	DisconnectTransport
	DisconnectHoldTimerExpired
	DisconnectAdministrative
	DisconnectAuthenticationFailure
	DisconnectNotification
)

const (
	DisconnectAuthFailure = DisconnectAuthenticationFailure
	AuthenticationFailure = DisconnectAuthenticationFailure
	AuthFailure           = DisconnectAuthenticationFailure
	DisconnectRestart     = DisconnectTransport
	DisconnectTCPFailure  = DisconnectTransport
)

func (r DisconnectReason) AuthenticationFailed() bool {
	return r == DisconnectAuthenticationFailure
}

func (r DisconnectReason) String() string {
	switch r {
	case DisconnectTransport:
		return "transport"
	case DisconnectHoldTimerExpired:
		return "hold-timer-expired"
	case DisconnectAdministrative:
		return "administrative"
	case DisconnectAuthenticationFailure:
		return "authentication-failure"
	case DisconnectNotification:
		return "notification"
	default:
		return "other"
	}
}

// Route is a route announcement.  ID is the route ID (Add-Path path ID or the
// host's internal route identifier).  Attributes is an opaque attribute
// digest/value; the ledger stores it verbatim so an auditor can compare the
// stale announcement with a refresh.
type Route struct {
	ID          RouteID `json:"id,omitempty"`
	RouteID     RouteID `json:"route_id,omitempty"`
	PathID      RouteID `json:"path_id,omitempty"`
	Prefix      Prefix  `json:"prefix"`
	NLRI        Prefix  `json:"-"`
	Attrs       string  `json:"attrs,omitempty"`
	Attributes  string  `json:"attributes,omitempty"`
	AttrsDigest string  `json:"attrs_digest,omitempty"`
	Digest      string  `json:"digest,omitempty"`
}

func (r Route) canonicalID() RouteID {
	switch {
	case strings.TrimSpace(r.ID) != "":
		return strings.TrimSpace(r.ID)
	case strings.TrimSpace(r.RouteID) != "":
		return strings.TrimSpace(r.RouteID)
	case strings.TrimSpace(r.PathID) != "":
		return strings.TrimSpace(r.PathID)
	default:
		return ""
	}
}

func (r Route) canonicalDigest() string {
	switch {
	case strings.TrimSpace(r.AttrsDigest) != "":
		return strings.TrimSpace(r.AttrsDigest)
	case strings.TrimSpace(r.Attributes) != "":
		return strings.TrimSpace(r.Attributes)
	case strings.TrimSpace(r.Attrs) != "":
		return strings.TrimSpace(r.Attrs)
	default:
		return strings.TrimSpace(r.Digest)
	}
}

func (r Route) normalized() Route {
	r.ID = r.canonicalID()
	r.RouteID = r.ID
	r.PathID = r.ID
	if r.Prefix == "" {
		r.Prefix = r.NLRI
	}
	r.Prefix = strings.TrimSpace(r.Prefix)
	r.NLRI = r.Prefix
	r.AttrsDigest = r.canonicalDigest()
	r.Attributes = r.AttrsDigest
	r.Attrs = r.AttrsDigest
	r.Digest = r.AttrsDigest
	return r
}

// RouteState is the security-relevant lifecycle state of a route.
type RouteState string

const (
	StateFresh     RouteState = "fresh"
	StateStale     RouteState = "stale"
	StateWithdrawn RouteState = "withdrawn"
)

// RouteView is an immutable snapshot returned by the ledger.
type RouteView struct {
	ID          RouteID `json:"id"`
	RouteID     RouteID `json:"route_id"`
	Prefix      Prefix  `json:"prefix"`
	AttrsDigest string  `json:"attrs_digest"`
	Attrs       string  `json:"attrs,omitempty"`
	Attributes  string  `json:"attributes,omitempty"`

	Peer                Peer     `json:"peer"`
	NeighborAS          ASN      `json:"neighbor_as"`
	RouterID            RouterID `json:"router_id"`
	TransportGeneration uint64   `json:"transport_generation"`
	SessionGeneration   uint64   `json:"session_generation"`

	State            RouteState `json:"state"`
	Stale            bool       `json:"stale"`
	Forwardable      bool       `json:"forwardable"`
	Exportable       bool       `json:"exportable"`
	FlowSpecEligible bool       `json:"flowspec_eligible"`
	StaleSince       time.Time  `json:"stale_since,omitempty"`
	Deadline         time.Time  `json:"stale_deadline,omitempty"`
	ExpiresAt        time.Time  `json:"expires_at,omitempty"`

	PolicyVersion              PolicyVersion `json:"policy_version"`
	VRPVersion                 VRPVersion    `json:"vrp_version"`
	OriginalPolicyVersion      PolicyVersion `json:"original_policy_version"`
	OriginalVRPVersion         VRPVersion    `json:"original_vrp_version"`
	LastValidatedPolicyVersion PolicyVersion `json:"last_validated_policy_version"`
	LastValidatedVRPVersion    VRPVersion    `json:"last_validated_vrp_version"`

	InstalledAt time.Time `json:"installed_at"`
}

// RouteEntry is an alias for RouteView.
type RouteEntry = RouteView

func (v RouteView) stale() bool { return v.State == StateStale }

// IsStale reports whether the snapshot is a stale route.
func (v RouteView) IsStale() bool { return v.State == StateStale }

// IsFresh reports whether the snapshot is a currently announced route.
func (v RouteView) IsFresh() bool { return v.State == StateFresh }

// StaleRoute is the durable stale-route record required for audit.  The
// original policy/VRP versions are preserved even when later revisions are
// used to revalidate the route.
type StaleRoute struct {
	RouteID     RouteID `json:"route_id"`
	ID          RouteID `json:"id"`
	Prefix      Prefix  `json:"prefix"`
	AttrsDigest string  `json:"attrs_digest"`
	Attrs       string  `json:"attrs"`
	Attributes  string  `json:"attributes"`

	Peer                Peer     `json:"peer"`
	NeighborAS          ASN      `json:"neighbor_as"`
	RouterID            RouterID `json:"router_id"`
	TransportGeneration uint64   `json:"transport_generation"`
	SessionGeneration   uint64   `json:"session_generation"`

	PolicyVersion              PolicyVersion `json:"policy_version"`
	VRPVersion                 VRPVersion    `json:"vrp_version"`
	OriginalPolicyVersion      PolicyVersion `json:"original_policy_version"`
	OriginalVRPVersion         VRPVersion    `json:"original_vrp_version"`
	OriginalPolicy             PolicyVersion `json:"-"`
	OriginalVRP                VRPVersion    `json:"-"`
	LastValidatedPolicyVersion PolicyVersion `json:"last_validated_policy_version"`
	LastValidatedVRPVersion    VRPVersion    `json:"last_validated_vrp_version"`

	StaleSince       time.Time  `json:"stale_since"`
	Deadline         time.Time  `json:"stale_deadline"`
	StaleUntil       time.Time  `json:"stale_until"`
	ExpiresAt        time.Time  `json:"expires_at"`
	State            RouteState `json:"state"`
	Forwardable      bool       `json:"forwardable"`
	Exportable       bool       `json:"exportable"`
	FlowSpecEligible bool       `json:"flowspec_eligible"`
}

// RouteUse describes why a route is being selected.  Stale routes are only
// eligible for local forwarding; they are never eligible for export or
// FlowSpec validation.
type RouteUse int

const (
	UseForward RouteUse = iota
	UseExport
	UseFlowSpec
)

const (
	UseLocalForward = UseForward
	UseAdvertise    = UseExport
	UseFlowspec     = UseFlowSpec
)

func (u RouteUse) String() string {
	switch u {
	case UseExport:
		return "export"
	case UseFlowSpec:
		return "flowspec"
	default:
		return "forward"
	}
}

// Selector is the host's route-selection callback.  It is only given routes
// that are eligible for the requested use.  The default selector prefers a
// fresh route over a stale one and otherwise chooses deterministically.
type Selector func(use RouteUse, candidates []RouteView) (RouteView, bool)

// WithdrawReason explains why a route left the forwarding table.
type WithdrawReason string

const (
	WithdrawExplicit    WithdrawReason = "explicit"
	WithdrawNoRestart   WithdrawReason = "no-restart-capability"
	WithdrawAuthFailure WithdrawReason = "authentication-failure"
	WithdrawPolicy      WithdrawReason = "policy"
	WithdrawVRP         WithdrawReason = "vrp"
	WithdrawDeadline    WithdrawReason = "restart-deadline"
	WithdrawEOR         WithdrawReason = "end-of-rib"
	WithdrawSuperseded  WithdrawReason = "superseded"
)

// Withdrawal is an audit record for a route that was removed.
type Withdrawal struct {
	RouteID       RouteID        `json:"route_id"`
	Prefix        Prefix         `json:"prefix"`
	AttrsDigest   string         `json:"attrs_digest"`
	Peer          Peer           `json:"peer"`
	Generation    uint64         `json:"transport_generation"`
	Reason        WithdrawReason `json:"reason"`
	At            time.Time      `json:"at"`
	PolicyVersion PolicyVersion  `json:"policy_version"`
	VRPVersion    VRPVersion     `json:"vrp_version"`
	WasStale      bool           `json:"was_stale"`
	Deadline      time.Time      `json:"stale_deadline,omitempty"`
}

// EventType identifies lifecycle notifications emitted by a Ledger.
type EventType string

const (
	EventStaled       EventType = "staled"
	EventRefreshed    EventType = "refreshed"
	EventWithdrawn    EventType = "withdrawn"
	EventExpired      EventType = "expired"
	EventPolicyUpdate EventType = "policy-update"
	EventVRPUpdate    EventType = "vrp-update"
	EventEOR          EventType = "end-of-rib"
)

// Event is an audit-friendly notification.  OnEvent callbacks must not call
// back into the same Ledger synchronously.
type Event struct {
	Type          EventType      `json:"type"`
	At            time.Time      `json:"at"`
	RouteID       RouteID        `json:"route_id,omitempty"`
	Prefix        Prefix         `json:"prefix,omitempty"`
	AttrsDigest   string         `json:"attrs_digest,omitempty"`
	Peer          Peer           `json:"peer"`
	Generation    uint64         `json:"transport_generation,omitempty"`
	Reason        WithdrawReason `json:"reason,omitempty"`
	Deadline      time.Time      `json:"stale_deadline,omitempty"`
	PolicyVersion PolicyVersion  `json:"policy_version"`
	VRPVersion    VRPVersion     `json:"vrp_version"`
}
