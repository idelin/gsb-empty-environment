package netsec

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	// DefaultQuietPeriod is how long a guarded port stays non-forwarding
	// after receiving a superior BPDU.
	DefaultQuietPeriod = 20 * time.Second

	// DefaultMaxAge is used when a BPDU does not carry a usable max-age
	// value.
	DefaultMaxAge = 20 * time.Second

	// DefaultTCNRateLimit is the default number of topology change
	// notifications accepted per port and interval.
	DefaultTCNRateLimit = 2

	// DefaultTCNRateInterval is the default TCN rate-limit window.
	DefaultTCNRateInterval = time.Second
)

// ErrUnknownPort is returned when a BPDU arrives on a port that has not been
// configured. Callers must not process a BPDU when HandleBPDU returns an
// error.
var ErrUnknownPort = errors.New("netsec: BPDU received on unconfigured port")

// PortState is the forwarding state enforced by the guard.
type PortState uint8

const (
	// PortForwarding means the port may forward data traffic.
	PortForwarding PortState = iota
	// PortNonForwarding means the port is blocked by a guard quiet period.
	PortNonForwarding
)

func (s PortState) String() string {
	switch s {
	case PortForwarding:
		return "forwarding"
	case PortNonForwarding:
		return "non-forwarding"
	default:
		return fmt.Sprintf("PortState(%d)", uint8(s))
	}
}

// PortConfig controls the receive-path protections for one port.
//
// Edge enables BPDU guard behavior: a configuration BPDU superior to the
// local bridge blocks the port. RootGuard blocks a configuration BPDU that is
// superior to the current root priority vector. A port may enable both.
type PortConfig struct {
	Edge      bool
	RootGuard bool
}

// GuardConfig configures a Guard. Zero values are replaced with secure
// defaults by NewGuard.
type GuardConfig struct {
	// LocalBridgeID is this bridge's identifier. It forms the initial
	// priority vector and must be set by the caller.
	LocalBridgeID BridgeID

	// QuietPeriod is how long a port remains non-forwarding after a
	// superior BPDU is rejected. A new superior BPDU restarts the period.
	QuietPeriod time.Duration

	// MaxAge is used for BPDUs whose MaxAge field is zero.
	MaxAge time.Duration

	// TCNRateLimit is the number of TCN BPDUs accepted per port in each
	// TCNRateInterval.
	TCNRateLimit int

	// TCNRateInterval is the fixed window used by TCNRateLimit.
	TCNRateInterval time.Duration

	// Clock supplies the current time. It is useful for deterministic
	// tests; nil means time.Now.
	Clock func() time.Time
}

// DropReason explains why HandleBPDU did not accept a BPDU.
type DropReason uint8

const (
	// DropNone means the BPDU was accepted.
	DropNone DropReason = iota
	// DropSuperiorBPDU means a guarded port received a superior
	// configuration BPDU.
	DropSuperiorBPDU
	// DropAgedBPDU means the message age was at least the maximum age.
	DropAgedBPDU
	// DropTCNRateLimit means the port's TCN allowance was exhausted.
	DropTCNRateLimit
	// DropUnsupportedBPDU means the BPDU type is not understood.
	DropUnsupportedBPDU
)

func (r DropReason) String() string {
	switch r {
	case DropNone:
		return "none"
	case DropSuperiorBPDU:
		return "superior BPDU"
	case DropAgedBPDU:
		return "aged BPDU"
	case DropTCNRateLimit:
		return "TCN rate limit"
	case DropUnsupportedBPDU:
		return "unsupported BPDU"
	default:
		return fmt.Sprintf("DropReason(%d)", uint8(r))
	}
}

// BPDUResult is the receive-path decision for one BPDU.
type BPDUResult struct {
	// Accepted reports whether the BPDU may be processed by the spanning
	// tree implementation.
	Accepted bool

	// Forwarding is the port's effective state after handling the BPDU.
	Forwarding bool

	// Reason is DropNone when Accepted is true.
	Reason DropReason

	// QuietUntil is the end of the current quiet period when Forwarding is
	// false, and the zero value otherwise.
	QuietUntil time.Time
}

type portState struct {
	config PortConfig

	blockedUntil time.Time

	tcnWindowStart time.Time
	tcnWindowSet   bool
	tcnCount       int
}

// Guard applies root-guard and edge-port protections on the BPDU receive
// path. It is safe for concurrent use by multiple receive goroutines.
type Guard struct {
	mu sync.Mutex

	config GuardConfig
	best   PriorityVector
	ports  map[PortID]*portState
}

// NewGuard creates a Guard. Invalid or zero durations and rate limits are
// replaced with defaults.
func NewGuard(config GuardConfig) *Guard {
	if config.QuietPeriod <= 0 {
		config.QuietPeriod = DefaultQuietPeriod
	}
	if config.MaxAge <= 0 {
		config.MaxAge = DefaultMaxAge
	}
	if config.TCNRateLimit <= 0 {
		config.TCNRateLimit = DefaultTCNRateLimit
	}
	if config.TCNRateInterval <= 0 {
		config.TCNRateInterval = DefaultTCNRateInterval
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}

	return &Guard{
		config: config,
		best: PriorityVector{
			RootID:         config.LocalBridgeID,
			SenderBridgeID: config.LocalBridgeID,
		},
		ports: make(map[PortID]*portState),
	}
}

// ConfigurePort adds a port or updates its guard configuration. Changing a
// configuration does not clear an active quiet period.
func (g *Guard) ConfigurePort(port PortID, config PortConfig) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if state, ok := g.ports[port]; ok {
		state.config = config
		return
	}
	g.ports[port] = &portState{config: config}
}

// HandleBPDU applies the receive-path guard. The caller must discard the BPDU
// when Accepted is false and must stop forwarding when Forwarding is false.
func (g *Guard) HandleBPDU(port PortID, bpdu BPDU) (BPDUResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	state, ok := g.ports[port]
	if !ok {
		return BPDUResult{}, fmt.Errorf("%w: %s", ErrUnknownPort, port)
	}

	now := g.config.Clock()
	if g.quietExpiredLocked(state, now) {
		state.blockedUntil = time.Time{}
	}

	maxAge := bpdu.MaxAge
	if maxAge <= 0 {
		maxAge = g.config.MaxAge
	}
	if bpdu.MessageAge < 0 || bpdu.MessageAge >= maxAge {
		return g.resultLocked(state, false, DropAgedBPDU), nil
	}

	switch bpdu.Type {
	case ConfigBPDU:
		vector := bpdu.PriorityVector()
		if state.config.Edge && vector.Compare(g.localVector(port)) < 0 {
			return g.blockLocked(state, now), nil
		}
		if state.config.RootGuard && vector.Compare(g.best) < 0 {
			return g.blockLocked(state, now), nil
		}

		if vector.Compare(g.best) < 0 {
			g.best = vector
		}
		return g.resultLocked(state, true, DropNone), nil

	case TCNBPDU:
		if !g.allowTCNLocked(state, now) {
			return g.resultLocked(state, false, DropTCNRateLimit), nil
		}
		return g.resultLocked(state, true, DropNone), nil

	default:
		return g.resultLocked(state, false, DropUnsupportedBPDU), nil
	}
}

// PortState returns the current forwarding state of a configured port.
func (g *Guard) PortState(port PortID) (PortState, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	state, ok := g.ports[port]
	if !ok {
		return PortForwarding, fmt.Errorf("%w: %s", ErrUnknownPort, port)
	}
	now := g.config.Clock()
	if g.quietExpiredLocked(state, now) {
		state.blockedUntil = time.Time{}
	}
	if state.blockedUntil.IsZero() {
		return PortForwarding, nil
	}
	return PortNonForwarding, nil
}

// QuietUntil returns the end of a port's active quiet period, or the zero time
// when the port is forwarding.
func (g *Guard) QuietUntil(port PortID) (time.Time, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	state, ok := g.ports[port]
	if !ok {
		return time.Time{}, fmt.Errorf("%w: %s", ErrUnknownPort, port)
	}
	now := g.config.Clock()
	if g.quietExpiredLocked(state, now) {
		state.blockedUntil = time.Time{}
	}
	return state.blockedUntil, nil
}

// BestVector returns the best priority vector learned from accepted
// configuration BPDUs, or the local vector when no superior BPDU has been
// accepted.
func (g *Guard) BestVector() PriorityVector {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.best
}

func (g *Guard) localVector(port PortID) PriorityVector {
	return PriorityVector{
		RootID:         g.config.LocalBridgeID,
		RootPathCost:   0,
		SenderBridgeID: g.config.LocalBridgeID,
		SenderPortID:   port,
	}
}

func (g *Guard) quietExpiredLocked(state *portState, now time.Time) bool {
	return !state.blockedUntil.IsZero() && !now.Before(state.blockedUntil)
}

func (g *Guard) blockLocked(state *portState, now time.Time) BPDUResult {
	state.blockedUntil = now.Add(g.config.QuietPeriod)
	return g.resultLocked(state, false, DropSuperiorBPDU)
}

func (g *Guard) resultLocked(state *portState, accepted bool, reason DropReason) BPDUResult {
	result := BPDUResult{
		Accepted:   accepted,
		Forwarding: state.blockedUntil.IsZero(),
		Reason:     reason,
	}
	if !result.Forwarding {
		result.QuietUntil = state.blockedUntil
	}
	return result
}

func (g *Guard) allowTCNLocked(state *portState, now time.Time) bool {
	if !state.tcnWindowSet ||
		now.Before(state.tcnWindowStart) ||
		now.Sub(state.tcnWindowStart) >= g.config.TCNRateInterval {
		state.tcnWindowStart = now
		state.tcnWindowSet = true
		state.tcnCount = 0
	}
	if state.tcnCount >= g.config.TCNRateLimit {
		return false
	}
	state.tcnCount++
	return true
}
