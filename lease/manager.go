// Package lease implements a single-machine simulation of a distributed
// lease coordination kernel with clock-drift tolerance.
//
// A lease is held by a leader node and has a definite TTL. The leader
// must account for the maximum clock drift and the communication
// round-trip time (RTT), and must voluntarily give up its privileges
// once the lease's safe period has passed:
//
//	safeWindow = LeaseDuration - MaxClockDrift - MaxRTT
//
// With a 10s TTL and 500ms max drift, the leader stops issuing writes
// as leader at t=9.5s. A watchdog self-check additionally detects
// sudden forward clock jumps larger than the tolerated drift and
// triggers an emergency early demotion, preventing split-brain.
package lease

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrLeaseHeld is returned by the Coordinator when another node still
// holds an unexpired lease.
var ErrLeaseHeld = errors.New("lease: held by another node")

// ErrNotLeader is returned when a node attempts to write without
// holding a valid lease inside its safe period.
var ErrNotLeader = errors.New("lease: not leader (lease missing, expired, or demoted)")

// Config tunes the lease kernel.
type Config struct {
	// LeaseDuration is the lease TTL (e.g. 10s).
	LeaseDuration time.Duration
	// MaxClockDrift is the maximum tolerated clock drift between nodes
	// (e.g. 500ms).
	MaxClockDrift time.Duration
	// MaxRTT is the worst-case communication round-trip delay between
	// a node and the coordinator.
	MaxRTT time.Duration
	// CheckInterval is how often the leader's watchdog self-checks.
	// Defaults to 50ms when <= 0.
	CheckInterval time.Duration
}

func (c Config) normalize() Config {
	if c.CheckInterval <= 0 {
		c.CheckInterval = 50 * time.Millisecond
	}
	return c
}

// SafeWindow returns how long after the grant a leader may safely act:
// LeaseDuration - MaxClockDrift - MaxRTT.
func (c Config) SafeWindow() time.Duration {
	return c.LeaseDuration - c.MaxClockDrift - c.MaxRTT
}

// Validate reports whether the configuration is usable.
func (c Config) Validate() error {
	if c.LeaseDuration <= 0 {
		return errors.New("lease: LeaseDuration must be positive")
	}
	if c.MaxClockDrift < 0 || c.MaxRTT < 0 {
		return errors.New("lease: drift and RTT bounds must be non-negative")
	}
	if c.SafeWindow() <= 0 {
		return errors.New("lease: safe window must be positive (TTL too small for drift+RTT)")
	}
	return nil
}

// Grant is the coordinator's record of an issued lease.
type Grant struct {
	Holder    string
	GrantedAt time.Time
	ExpiresAt time.Time
}

// Coordinator is the lease grant server (single-machine simulation).
// It guarantees at most one unexpired lease exists at any moment.
type Coordinator struct {
	mu        sync.Mutex
	clock     Clock
	cfg       Config
	holder    string
	expiresAt time.Time
}

// NewCoordinator creates a Coordinator using the given clock and config.
func NewCoordinator(clock Clock, cfg Config) *Coordinator {
	return &Coordinator{clock: clock, cfg: cfg.normalize()}
}

// Acquire grants the lease to nodeID if no other node's lease is live.
func (c *Coordinator) Acquire(nodeID string) (Grant, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock.Now()
	if c.holder != "" && c.holder != nodeID && now.Before(c.expiresAt) {
		return Grant{}, fmt.Errorf("%w (holder=%q, expires in %s)",
			ErrLeaseHeld, c.holder, c.expiresAt.Sub(now).Round(time.Millisecond))
	}
	c.holder = nodeID
	c.expiresAt = now.Add(c.cfg.LeaseDuration)
	return Grant{Holder: nodeID, GrantedAt: now, ExpiresAt: c.expiresAt}, nil
}

// Release lets nodeID give up the lease early (used on demotion).
func (c *Coordinator) Release(nodeID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.holder == nodeID {
		c.holder = ""
		c.expiresAt = time.Time{}
	}
}

// Manager is the client-side lease holder running on one node. While it
// holds the lease and is inside the safe window it may act as leader.
type Manager struct {
	nodeID string
	cfg    Config
	clock  Clock
	coord  *Coordinator

	mu          sync.Mutex
	held        bool
	demoted     bool
	reason      string
	safeUntil   time.Time
	lastChecked time.Time

	stopCh chan struct{}
	doneCh chan struct{}
}

// NewManager creates a lease manager for nodeID.
func NewManager(nodeID string, clock Clock, coord *Coordinator, cfg Config) *Manager {
	return &Manager{
		nodeID: nodeID,
		cfg:    cfg.normalize(),
		clock:  clock,
		coord:  coord,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// Acquire obtains the lease from the coordinator and starts the
// watchdog. The safe deadline conservatively subtracts the max clock
// drift and the worst-case RTT from the TTL.
func (m *Manager) Acquire() error {
	grant, err := m.coord.Acquire(m.nodeID)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.held = true
	m.demoted = false
	m.reason = ""
	m.safeUntil = grant.GrantedAt.Add(m.cfg.SafeWindow())
	m.lastChecked = m.clock.Now()
	m.mu.Unlock()
	go m.watchdog()
	return nil
}

// watchdog periodically runs the safety self-check until Stop is called.
func (m *Manager) watchdog() {
	defer close(m.doneCh)
	ticker := time.NewTicker(m.cfg.CheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.SelfCheck()
		}
	}
}

// SelfCheck performs the leader's safety self-check. It is called by the
// watchdog and may also be called synchronously (e.g. from tests):
//
//  1. Clock-jump detection: if the local clock advanced by more than
//     CheckInterval+MaxClockDrift since the previous check, the clock is
//     untrustworthy -> emergency early demotion.
//  2. Safe-period enforcement: once the safe window has passed, the
//     leader voluntarily demotes itself before the TTL actually expires.
func (m *Manager) SelfCheck() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.held {
		return
	}
	now := m.clock.Now()
	elapsed := now.Sub(m.lastChecked)
	if elapsed > m.cfg.CheckInterval+m.cfg.MaxClockDrift {
		m.demoteLocked(fmt.Sprintf("emergency: clock jumped forward %s since last self-check (tolerance %s)",
			elapsed.Round(time.Millisecond), (m.cfg.CheckInterval + m.cfg.MaxClockDrift).Round(time.Millisecond)))
		return
	}
	m.lastChecked = now
	if !now.Before(m.safeUntil) {
		m.demoteLocked(fmt.Sprintf("lease safe period (%s) elapsed; TTL not yet expired",
			m.cfg.SafeWindow()))
	}
}

func (m *Manager) demoteLocked(reason string) {
	m.held = false
	m.demoted = true
	m.reason = reason
	m.coord.Release(m.nodeID)
}

// CanWrite reports whether the node may currently issue writes as leader.
func (m *Manager) CanWrite() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.held && m.clock.Now().Before(m.safeUntil)
}

// Write issues a write as leader. It fails with ErrNotLeader unless the
// lease is held and the clock is still inside the safe window.
func (m *Manager) Write(op string) error {
	if !m.CanWrite() {
		return ErrNotLeader
	}
	return nil
}

// Status describes the manager's current state.
type Status struct {
	Held      bool
	Demoted   bool
	Reason    string
	SafeUntil time.Time
}

// Status returns a snapshot of the manager state.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Status{Held: m.held, Demoted: m.demoted, Reason: m.reason, SafeUntil: m.safeUntil}
}

// Stop halts the watchdog goroutine.
func (m *Manager) Stop() {
	select {
	case <-m.doneCh:
		return
	default:
	}
	close(m.stopCh)
	<-m.doneCh
}
