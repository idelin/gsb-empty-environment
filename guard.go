package netsec

import (
	"bytes"
	"fmt"
	"sync"
	"time"
)

// Default timer and rate-limit values used when a GuardConfig leaves the
// corresponding field unset.
const (
	DefaultQuietPeriod = 30 * time.Second
	DefaultMaxAge      = 20 * time.Second
	DefaultTCNRate     = 1.0
	DefaultTCNBurst    = 1
)

// BridgeID is an IEEE 802.1D bridge identifier. Lower values are better:
// priority is compared first, then the MAC address.
type BridgeID struct {
	Priority uint16
	MAC      [6]byte
}

// Compare returns -1 when b is better than other, 1 when it is worse, and 0
// when the identifiers are equal.
func (b BridgeID) Compare(other BridgeID) int {
	if b.Priority < other.Priority {
		return -1
	}
	if b.Priority > other.Priority {
		return 1
	}
	return bytes.Compare(b.MAC[:], other.MAC[:])
}

// Less reports whether b has a better (numerically lower) bridge ID.
func (b BridgeID) Less(other BridgeID) bool {
	return b.Compare(other) < 0
}

func (b BridgeID) isZero() bool {
	return b.Priority == 0 && b.MAC == [6]byte{}
}

func (b BridgeID) String() string {
	return fmt.Sprintf("%d/%02x:%02x:%02x:%02x:%02x:%02x",
		b.Priority, b.MAC[0], b.MAC[1], b.MAC[2], b.MAC[3], b.MAC[4], b.MAC[5])
}

// PortID identifies a sending port. The numeric 802.1D encoding (priority in
// the high bits and port number in the low bits) compares correctly as an
// unsigned integer.
type PortID uint16

// Compare returns -1 when p is better than other, 1 when it is worse, and 0
// when they are equal.
func (p PortID) Compare(other PortID) int {
	switch {
	case p < other:
		return -1
	case p > other:
		return 1
	default:
		return 0
	}
}

// PriorityVector is the STP priority vector used to elect the root bridge and
// the root port. Comparison is lexicographic in this exact field order.
type PriorityVector struct {
	RootID         BridgeID
	RootPathCost   uint32
	SenderBridgeID BridgeID
	SenderPortID   PortID
}

// Compare returns -1 when v is better than other, 1 when it is worse, and 0
// when the vectors are equal.
func (v PriorityVector) Compare(other PriorityVector) int {
	if c := v.RootID.Compare(other.RootID); c != 0 {
		return c
	}
	switch {
	case v.RootPathCost < other.RootPathCost:
		return -1
	case v.RootPathCost > other.RootPathCost:
		return 1
	}
	if c := v.SenderBridgeID.Compare(other.SenderBridgeID); c != 0 {
		return c
	}
	return v.SenderPortID.Compare(other.SenderPortID)
}

// SuperiorTo reports whether v is strictly better than other.
func (v PriorityVector) SuperiorTo(other PriorityVector) bool {
	return v.Compare(other) < 0
}

// Equal reports whether all priority-vector fields match.
func (v PriorityVector) Equal(other PriorityVector) bool {
	return v.Compare(other) == 0
}

// BPDUType is the STP BPDU type carried in the protocol discriminator byte.
type BPDUType uint8

const (
	// ConfigurationBPDU carries the root/designated priority information.
	ConfigurationBPDU BPDUType = 0x00
	// TopologyChangeNotificationBPDU is a TCN BPDU.
	TopologyChangeNotificationBPDU BPDUType = 0x80

	// Short aliases for callers that parse raw 802.1D frames.
	ConfigBPDU = ConfigurationBPDU
	TCNBPDU    = TopologyChangeNotificationBPDU
)

// BPDU is the decoded subset of an STP BPDU needed by the receive-path guard.
type BPDU struct {
	Type BPDUType

	RootID         BridgeID
	RootPathCost   uint32
	SenderBridgeID BridgeID
	SenderPortID   PortID

	MessageAge time.Duration
	MaxAge     time.Duration

	// TopologyChange is the topology-change flag of a configuration BPDU.
	// A TCN BPDU is identified by Type and may leave this false.
	TopologyChange bool
}

// Vector returns the priority vector advertised by a configuration BPDU.
func (b BPDU) Vector() PriorityVector {
	return PriorityVector{
		RootID:         b.RootID,
		RootPathCost:   b.RootPathCost,
		SenderBridgeID: b.SenderBridgeID,
		SenderPortID:   b.SenderPortID,
	}
}

// PriorityVector is an alias for Vector.
func (b BPDU) PriorityVector() PriorityVector {
	return b.Vector()
}

// IsTCN reports whether the BPDU signals a topology change.
func (b BPDU) IsTCN() bool {
	return b.Type == TCNBPDU || b.TopologyChange
}

// PortConfig describes the security role of an ingress switch port.
type PortConfig struct {
	ID        PortID
	Edge      bool
	RootGuard bool
}

// PortState is the forwarding state enforced by the guard.
type PortState uint8

const (
	StateForwarding PortState = iota
	StateBlocking
)

func (s PortState) String() string {
	switch s {
	case StateForwarding:
		return "forwarding"
	case StateBlocking:
		return "blocking"
	default:
		return fmt.Sprintf("PortState(%d)", uint8(s))
	}
}

// DropReason explains why a BPDU was not accepted.
type DropReason uint8

const (
	ReasonNone DropReason = iota
	ReasonMessageAgeExpired
	ReasonEdgeGuard
	ReasonRootGuard
	ReasonPortBlocked
	ReasonTCNRateLimit
	ReasonUnsupportedBPDU
)

func (r DropReason) String() string {
	switch r {
	case ReasonNone:
		return "accepted"
	case ReasonMessageAgeExpired:
		return "message age reached max age"
	case ReasonEdgeGuard:
		return "superior BPDU on edge port"
	case ReasonRootGuard:
		return "superior BPDU on root-guard port"
	case ReasonPortBlocked:
		return "port is in guard quiet period"
	case ReasonTCNRateLimit:
		return "TCN rate limit exceeded"
	case ReasonUnsupportedBPDU:
		return "unsupported BPDU type"
	default:
		return fmt.Sprintf("DropReason(%d)", uint8(r))
	}
}

// Result is returned to the BPDU receive path.
type Result struct {
	// Accepted is true when the BPDU may be processed by STP.
	Accepted bool
	// Forwarding is the port forwarding state after processing the BPDU.
	Forwarding bool
	// State is the resulting port state.
	State PortState
	// Reason is ReasonNone for an accepted BPDU.
	Reason DropReason
	// BlockedUntil is the end of the quiet period. It is zero when the port is
	// not blocked by a guard violation.
	BlockedUntil time.Time
}

// PortStats contains per-port counters maintained by the guard.
type PortStats struct {
	Accepted        uint64
	Dropped         uint64
	TCNs            uint64
	TCNRateLimited  uint64
	GuardViolations uint64
}

// GuardConfig configures a BPDU guard.
type GuardConfig struct {
	// LocalBridgeID is this switch's own bridge ID. If it is the zero value,
	// NewGuard substitutes a non-root default so an unconfigured guard still
	// does not treat every bridge as inferior.
	LocalBridgeID BridgeID
	// LocalRootPathCost is normally zero when the local bridge is the root.
	LocalRootPathCost uint32
	// RootVector is the currently known root path vector. If nil, the guard
	// starts with the local bridge as root and learns better vectors from
	// accepted configuration BPDUs.
	RootVector *PriorityVector

	// QuietPeriod is how long a guarded port remains non-forwarding after a
	// superior BPDU. It is extended by each subsequent superior BPDU.
	QuietPeriod time.Duration
	// MaxAge is the local maximum message age. A BPDU advertising a smaller
	// MaxAge is held to that smaller value.
	MaxAge time.Duration
	// TCNRate is the sustained number of accepted TCNs per second per port.
	TCNRate float64
	// TCNBurst is the maximum TCN token-bucket depth per port.
	TCNBurst int
	// Now is injectable for tests. It defaults to time.Now.
	Now func() time.Time
}

// Guard applies root-guard/BPDU-guard and TCN admission policy to BPDUs
// arriving on switch ports. It is safe for concurrent use.
type Guard struct {
	mu sync.Mutex

	currentRoot PriorityVector
	quietPeriod time.Duration
	maxAge      time.Duration
	tcnRate     float64
	tcnBurst    int
	now         func() time.Time

	ports map[PortID]*portRuntime
}

type portRuntime struct {
	id        PortID
	edge      bool
	rootGuard bool

	blockedUntil time.Time
	tcnBucket    tokenBucket
	stats        PortStats
}

type tokenBucket struct {
	initialized bool
	tokens      float64
	last        time.Time
}

func (b *tokenBucket) allow(now time.Time, rate float64, burst int) bool {
	if !b.initialized {
		b.initialized = true
		b.tokens = float64(burst)
		b.last = now
	}

	elapsed := now.Sub(b.last)
	if elapsed > 0 {
		b.tokens += elapsed.Seconds() * rate
		if b.tokens > float64(burst) {
			b.tokens = float64(burst)
		}
		b.last = now
	}

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// NewGuard constructs a guard and applies secure defaults to unset fields.
func NewGuard(cfg GuardConfig) *Guard {
	if cfg.LocalBridgeID.isZero() {
		// 0x8000 is the conventional default priority. The locally
		// administered address keeps the fallback bridge non-root.
		cfg.LocalBridgeID = BridgeID{
			Priority: 0x8000,
			MAC:      [6]byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x01},
		}
	}
	if cfg.QuietPeriod <= 0 {
		cfg.QuietPeriod = DefaultQuietPeriod
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = DefaultMaxAge
	}
	if cfg.TCNRate <= 0 {
		cfg.TCNRate = DefaultTCNRate
	}
	if cfg.TCNBurst <= 0 {
		cfg.TCNBurst = DefaultTCNBurst
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	root := PriorityVector{
		RootID:         cfg.LocalBridgeID,
		RootPathCost:   cfg.LocalRootPathCost,
		SenderBridgeID: cfg.LocalBridgeID,
	}
	if cfg.RootVector != nil {
		root = *cfg.RootVector
	}

	return &Guard{
		currentRoot: root,
		quietPeriod: cfg.QuietPeriod,
		maxAge:      cfg.MaxAge,
		tcnRate:     cfg.TCNRate,
		tcnBurst:    cfg.TCNBurst,
		now:         cfg.Now,
		ports:       make(map[PortID]*portRuntime),
	}
}

// AddPort registers or replaces a port configuration. Unknown ingress ports
// are treated as ordinary forwarding ports by Receive.
func (g *Guard) AddPort(cfg PortConfig) *Guard {
	g.mu.Lock()
	defer g.mu.Unlock()

	p := g.ports[cfg.ID]
	if p == nil {
		p = &portRuntime{id: cfg.ID}
		g.ports[cfg.ID] = p
	}
	p.edge = cfg.Edge
	p.rootGuard = cfg.RootGuard
	return g
}

// ConfigurePort is an alias for AddPort.
func (g *Guard) ConfigurePort(cfg PortConfig) *Guard {
	return g.AddPort(cfg)
}

// Receive inspects a BPDU arriving on ingress. The caller should process the
// BPDU through STP only when Accepted is true. A guarded port that receives a
// superior vector is moved to blocking for at least QuietPeriod.
func (g *Guard) Receive(ingress PortID, bpdu BPDU) Result {
	g.mu.Lock()
	defer g.mu.Unlock()

	p := g.ports[ingress]
	if p == nil {
		p = &portRuntime{id: ingress}
		g.ports[ingress] = p
	}

	now := g.now()
	g.expireBlock(p, now)

	if g.messageAgeExpired(bpdu) {
		p.stats.Dropped++
		return Result{
			State:  g.state(p, now),
			Reason: ReasonMessageAgeExpired,
		}
	}

	guardPort := p.edge || p.rootGuard
	if guardPort && bpdu.Type == ConfigurationBPDU {
		if bpdu.Vector().SuperiorTo(g.currentRoot) {
			p.blockedUntil = now.Add(g.quietPeriod)
			p.stats.GuardViolations++
			p.stats.Dropped++

			reason := ReasonRootGuard
			if p.edge {
				reason = ReasonEdgeGuard
			}
			return Result{
				Forwarding:   false,
				State:        StateBlocking,
				Reason:       reason,
				BlockedUntil: p.blockedUntil,
			}
		}
	}

	if g.isBlocked(p, now) {
		p.stats.Dropped++
		return Result{
			State:        StateBlocking,
			Reason:       ReasonPortBlocked,
			BlockedUntil: p.blockedUntil,
		}
	}

	switch bpdu.Type {
	case ConfigurationBPDU:
		if bpdu.TopologyChange {
			p.stats.TCNs++
			if !p.tcnBucket.allow(now, g.tcnRate, g.tcnBurst) {
				p.stats.TCNRateLimited++
				p.stats.Dropped++
				return Result{
					Forwarding: true,
					State:      StateForwarding,
					Reason:     ReasonTCNRateLimit,
				}
			}
		}

		// Learn a better root only after admission checks. In particular, a
		// forged superior vector on a guarded port never becomes the baseline
		// used by later decisions.
		if bpdu.Vector().SuperiorTo(g.currentRoot) {
			g.currentRoot = bpdu.Vector()
		}
		p.stats.Accepted++
		return Result{
			Accepted:   true,
			Forwarding: true,
			State:      StateForwarding,
			Reason:     ReasonNone,
		}

	case TCNBPDU:
		p.stats.TCNs++
		if !p.tcnBucket.allow(now, g.tcnRate, g.tcnBurst) {
			p.stats.TCNRateLimited++
			p.stats.Dropped++
			return Result{
				Forwarding: true,
				State:      StateForwarding,
				Reason:     ReasonTCNRateLimit,
			}
		}
		p.stats.Accepted++
		return Result{
			Accepted:   true,
			Forwarding: true,
			State:      StateForwarding,
			Reason:     ReasonNone,
		}

	default:
		p.stats.Dropped++
		return Result{
			State:  StateForwarding,
			Reason: ReasonUnsupportedBPDU,
		}
	}
}

// ProcessBPDU is an alias for Receive for switch receive paths that use that
// terminology.
func (g *Guard) ProcessBPDU(ingress PortID, bpdu BPDU) Result {
	return g.Receive(ingress, bpdu)
}

// PortState returns the current forwarding state. An expired quiet period is
// cleared lazily.
func (g *Guard) PortState(id PortID) PortState {
	g.mu.Lock()
	defer g.mu.Unlock()

	p := g.ports[id]
	if p == nil {
		return StateForwarding
	}
	now := g.now()
	g.expireBlock(p, now)
	return g.state(p, now)
}

// PortBlockedUntil returns the end of the current quiet period, or the zero
// time if the port is not blocked.
func (g *Guard) PortBlockedUntil(id PortID) time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()

	p := g.ports[id]
	if p == nil {
		return time.Time{}
	}
	now := g.now()
	g.expireBlock(p, now)
	if !g.isBlocked(p, now) {
		return time.Time{}
	}
	return p.blockedUntil
}

// PortCounters returns a snapshot of the port's counters.
func (g *Guard) PortCounters(id PortID) PortStats {
	g.mu.Lock()
	defer g.mu.Unlock()

	if p := g.ports[id]; p != nil {
		return p.stats
	}
	return PortStats{}
}

// CurrentRootVector returns the guard's current comparison baseline.
func (g *Guard) CurrentRootVector() PriorityVector {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.currentRoot
}

func (g *Guard) messageAgeExpired(bpdu BPDU) bool {
	maxAge := g.maxAge
	if bpdu.MaxAge > 0 && bpdu.MaxAge < maxAge {
		maxAge = bpdu.MaxAge
	}
	return bpdu.MessageAge >= maxAge
}

func (g *Guard) expireBlock(p *portRuntime, now time.Time) {
	if !p.blockedUntil.IsZero() && !now.Before(p.blockedUntil) {
		p.blockedUntil = time.Time{}
	}
}

func (g *Guard) isBlocked(p *portRuntime, now time.Time) bool {
	return !p.blockedUntil.IsZero() && now.Before(p.blockedUntil)
}

func (g *Guard) state(p *portRuntime, now time.Time) PortState {
	if g.isBlocked(p, now) {
		return StateBlocking
	}
	return StateForwarding
}
