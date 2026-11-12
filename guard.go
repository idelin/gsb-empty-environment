package netsec

import (
	"fmt"
	"time"
)

// Verdict tells the BPDU receive path what to do with an incoming BPDU.
type Verdict int

const (
	// Accept lets the BPDU continue through normal STP processing.
	Accept Verdict = iota
	// DropStale discards BPDUs whose MessageAge has reached MaxAge.
	DropStale
	// DropRateLimited discards TCN BPDUs exceeding the per-port rate limit.
	DropRateLimited
	// DropGuarded discards a superior BPDU received on an edge or
	// root-guard port and forces the port to stop forwarding.
	DropGuarded
	// DropSilenced discards BPDUs arriving while the port is serving
	// its guard-imposed silent period.
	DropSilenced
)

func (v Verdict) String() string {
	switch v {
	case Accept:
		return "accept"
	case DropStale:
		return "drop-stale"
	case DropRateLimited:
		return "drop-rate-limited"
	case DropGuarded:
		return "drop-guarded"
	case DropSilenced:
		return "drop-silenced"
	}
	return "unknown"
}

// PortConfig holds the operator-configured protection knobs for one port.
type PortConfig struct {
	// Edge marks a port-fast edge port: only end hosts are expected
	// here, so any superior BPDU is treated as an attack (BPDU guard).
	Edge bool
	// RootGuard prevents the port from ever becoming a root port:
	// any superior BPDU is treated as a rogue root attempt.
	RootGuard bool
	// SilentPeriod is how long the port stops forwarding after a
	// guard violation. Zero uses DefaultSilentPeriod.
	SilentPeriod time.Duration
	// TCNRate is the sustained TCN BPDUs per second allowed on the
	// port. Zero disables TCN admission entirely; use TCNBurst for
	// the burst allowance.
	TCNRate float64
	// TCNBurst is the token-bucket capacity for TCN admission.
	// Zero with a positive TCNRate defaults to 1.
	TCNBurst int
}

// DefaultSilentPeriod is the guard hold-down used when a port does not
// configure its own.
const DefaultSilentPeriod = 30 * time.Second

// tokenBucket is a simple per-port rate limiter.
type tokenBucket struct {
	rate     float64 // tokens per second
	capacity float64
	tokens   float64
	last     time.Time
}

func newTokenBucket(rate float64, burst int, now time.Time) *tokenBucket {
	cap := float64(burst)
	if cap <= 0 {
		cap = 1
	}
	return &tokenBucket{rate: rate, capacity: cap, tokens: cap, last: now}
}

func (b *tokenBucket) allow(now time.Time) bool {
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * b.rate
		if b.tokens > b.capacity {
			b.tokens = b.capacity
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// portState is the mutable per-port guard state.
type portState struct {
	cfg          PortConfig
	best         PriorityVector // best vector seen from a legitimate peer
	hasBest      bool
	silencedTill time.Time // port stops forwarding until this time
	tcnBucket    *tokenBucket
}

// Guard protects the STP BPDU receive path against rogue-root and
// TCN-flood attacks. It is not safe for concurrent use; serialize
// calls per switch instance (the receive path normally is).
type Guard struct {
	ports map[string]*portState
}

// NewGuard returns an empty Guard.
func NewGuard() *Guard {
	return &Guard{ports: make(map[string]*portState)}
}

// ConfigurePort installs or replaces the protection config of a port.
func (g *Guard) ConfigurePort(port string, cfg PortConfig) {
	if cfg.SilentPeriod <= 0 {
		cfg.SilentPeriod = DefaultSilentPeriod
	}
	g.ports[port] = &portState{cfg: cfg}
}

// RemovePort drops all guard state for a port (link down, port removed).
func (g *Guard) RemovePort(port string) {
	delete(g.ports, port)
}

// Forwarding reports whether the port is currently allowed to forward
// data frames. A port under a guard violation stops forwarding until
// its silent period expires.
func (g *Guard) Forwarding(port string, now time.Time) bool {
	st, ok := g.ports[port]
	if !ok {
		return true
	}
	return !now.Before(st.silencedTill)
}

// Process inspects one received BPDU and returns the verdict for the
// receive path. now is injected so tests can control time.
func (g *Guard) Process(port string, bpdu BPDU, now time.Time) Verdict {
	st, ok := g.ports[port]
	if !ok {
		// Unmanaged port: apply only the sanity checks that need no state.
		if bpdu.MessageAge >= bpdu.MaxAge {
			return DropStale
		}
		return Accept
	}

	// Stale information is never usable, guard state or not.
	if bpdu.MessageAge >= bpdu.MaxAge {
		return DropStale
	}

	// While silenced the port ignores everything; the silent period
	// only counts down, it is not extended by repeated attacks.
	if now.Before(st.silencedTill) {
		return DropSilenced
	}

	if bpdu.Type == BPDUTypeTCN {
		if st.tcnBucket == nil {
			st.tcnBucket = newTokenBucket(st.cfg.TCNRate, st.cfg.TCNBurst, now)
		}
		if st.cfg.TCNRate <= 0 || !st.tcnBucket.allow(now) {
			return DropRateLimited
		}
		return Accept
	}

	vec := bpdu.Vector()

	// On edge or root-guard ports, a superior BPDU means someone is
	// trying to become (or move) the root through this port: drop it
	// and stop the port from forwarding for the silent period.
	if st.cfg.Edge || st.cfg.RootGuard {
		if !st.hasBest || vec.Superior(st.best) {
			if st.hasBest || st.cfg.Edge {
				st.silencedTill = now.Add(st.cfg.SilentPeriod)
				return DropGuarded
			}
		}
	}

	// Track the best vector accepted from this peer so later, more
	// preferred claims can be judged against it.
	if !st.hasBest || vec.Superior(st.best) {
		st.best = vec
		st.hasBest = true
	}
	return Accept
}

// String summarizes the guard for diagnostics.
func (g *Guard) String() string {
	return fmt.Sprintf("Guard{ports:%d}", len(g.ports))
}
