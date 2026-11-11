package netsec

import (
	"net/netip"
	"sync"
	"time"
)

// Config tunes the neighbor-unreachability timers and retention bounds.
type Config struct {
	// ReachableTTL is how long a confirmed binding is trusted without a
	// fresh ARP confirmation before it becomes stale.
	ReachableTTL time.Duration
	// ProbeInterval is the spacing between ARP probes.
	ProbeInterval time.Duration
	// MaxProbes is the number of unanswered probes before resolution fails
	// or a stale binding is invalidated.
	MaxProbes uint8
	// MaxEvidencePerIP bounds the forensic ring for each quarantined IP.
	MaxEvidencePerIP int
	// MaxEvents bounds the global audit-event ring kept for the host.
	MaxEvents int
}

// DefaultConfig returns production-safe timer defaults.
func DefaultConfig() Config {
	return Config{
		ReachableTTL:     30 * time.Second,
		ProbeInterval:    time.Second,
		MaxProbes:        3,
		MaxEvidencePerIP: 16,
		MaxEvents:        4096,
	}
}

// NeighborView is a lock-free snapshot of one neighbor.
type NeighborView struct {
	IP           netip.Addr
	State        State
	HasTicket    bool
	TicketMAC    MAC
	TicketGen    uint64
	TicketExpire time.Time
	HasBinding   bool
	BoundMAC     MAC
	BoundPort    PortID
	BoundGen     uint64
	ConfirmedAt  time.Time
	Probes       uint8
}

// Switch is the neighbor state machine a virtual-switch host embeds. It is
// safe for concurrent use: the trusted control plane calls Grant/Revoke while
// the data plane calls ObserveARP and Forward concurrently.
type Switch struct {
	mu     sync.RWMutex
	cfg    Config
	now    func() time.Time
	seq    uint64
	table  map[netip.Addr]*neighbor
	events []Event
}

// NewSwitch builds a Switch using the real wall clock and default config.
func NewSwitch() *Switch {
	return NewSwitchWithClock(DefaultConfig(), time.Now)
}

// NewSwitchWithClock builds a Switch with an explicit clock (tests inject a
// fake clock) and config.
func NewSwitchWithClock(cfg Config, clock func() time.Time) *Switch {
	if cfg.ReachableTTL <= 0 || cfg.ProbeInterval <= 0 || cfg.MaxProbes == 0 {
		cfg = DefaultConfig()
	}
	if cfg.MaxEvidencePerIP <= 0 {
		cfg.MaxEvidencePerIP = 16
	}
	if cfg.MaxEvents <= 0 {
		cfg.MaxEvents = 4096
	}
	return &Switch{
		cfg:   cfg,
		now:   clock,
		table: make(map[netip.Addr]*neighbor),
	}
}

// nextSeq returns a monotonically increasing sequence number. Callers hold mu.
func (s *Switch) nextSeq() uint64 {
	s.seq++
	return s.seq
}

func (s *Switch) record(evs []Event) {
	for _, e := range evs {
		s.events = append(s.events, e)
		if s.cfg.MaxEvents > 0 && len(s.events) > s.cfg.MaxEvents {
			s.events = s.events[len(s.events)-s.cfg.MaxEvents:]
		}
	}
}

func (s *Switch) getOrCreate(ip netip.Addr) *neighbor {
	n, ok := s.table[ip]
	if !ok {
		n = newNeighbor(ip)
		s.table[ip] = n
	}
	return n
}

// Grant installs a trusted control-plane migration ticket. A ticket with a
// generation equal to or older than the current one is rejected and leaves all
// state untouched.
func (s *Switch) Grant(t Ticket) error {
	if !t.IP.IsValid() {
		return ErrInvalidIP
	}
	if !t.MAC.Valid() {
		return ErrInvalidMAC
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	n := s.getOrCreate(t.IP)
	evs, err := n.grant(t, s.now(), s.nextSeq, s.cfg.MaxEvidencePerIP)
	s.record(evs)
	return err
}

// Revoke removes the active ticket for an IP. Existing bindings become stale
// but forensic evidence is retained; quarantine is preserved.
func (s *Switch) Revoke(ip netip.Addr) error {
	if !ip.IsValid() {
		return ErrInvalidIP
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	n, ok := s.table[ip]
	if !ok {
		return ErrUnknownNeighbor
	}
	evs, err := n.revoke(s.now(), s.nextSeq)
	s.record(evs)
	return err
}

// ObserveARP feeds the data plane an ARP claim (sender MAC/port for an IP).
// It returns why the claim was accepted or rejected. Unauthorized claims
// quarantine the IP and retain evidence.
func (s *Switch) ObserveARP(ip netip.Addr, mac MAC, port PortID) Reason {
	if !ip.IsValid() || !mac.Valid() {
		return ReasonNoTicket
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	n := s.getOrCreate(ip)
	reason, evs := n.observeARP(arpObservation{mac: mac, port: port}, s.now(), s.nextSeq, s.cfg.MaxEvidencePerIP)
	s.record(evs)
	return reason
}

// StartResolution begins ARP resolution for an authorized, unbound IP.
func (s *Switch) StartResolution(ip netip.Addr) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	n, ok := s.table[ip]
	if !ok {
		return ErrUnknownNeighbor
	}
	evs, err := n.startResolution(s.now(), s.cfg.ProbeInterval, s.nextSeq)
	s.record(evs)
	return err
}

// Revalidate starts re-probing a stale but still authorized binding.
func (s *Switch) Revalidate(ip netip.Addr) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	n, ok := s.table[ip]
	if !ok {
		return ErrUnknownNeighbor
	}
	evs, err := n.revalidate(s.now(), s.cfg.ProbeInterval, s.nextSeq)
	s.record(evs)
	return err
}

// Forward decides what the data plane does with a frame destined to ip.
// Reachable neighbors forward to their bound port; quarantined neighbors drop;
// every other state floods.
func (s *Switch) Forward(ip netip.Addr) (FrameVerdict, PortID) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n, ok := s.table[ip]
	if !ok {
		return VerdictFlood, 0
	}
	return n.verdict()
}

// Tick advances all reachability and probe timers to the injected clock.
func (s *Switch) Tick() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	var emitted []Event
	for _, n := range s.table {
		emitted = append(emitted, n.tick(now, s.cfg, s.nextSeq)...)
	}
	s.record(emitted)
	return emitted
}

// Snapshot returns a point-in-time copy of one neighbor.
func (s *Switch) Snapshot(ip netip.Addr) (NeighborView, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n, ok := s.table[ip]
	if !ok {
		return NeighborView{}, false
	}
	return NeighborView{
		IP:           n.ip,
		State:        n.state,
		HasTicket:    n.hasTicket,
		TicketMAC:    n.ticketMAC,
		TicketGen:    n.ticketGen,
		TicketExpire: n.ticketExpires,
		HasBinding:   n.hasBinding,
		BoundMAC:     n.bindMAC,
		BoundPort:    n.bindPort,
		BoundGen:     n.bindGen,
		ConfirmedAt:  n.confirmedAt,
		Probes:       n.probes,
	}, true
}

// State returns just the current state of a neighbor (StateEmpty if unknown).
func (s *Switch) State(ip netip.Addr) State {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n, ok := s.table[ip]
	if !ok {
		return StateEmpty
	}
	return n.state
}

// Evidence returns a copy of the retained forensic records for an IP.
func (s *Switch) Evidence(ip netip.Addr) []Evidence {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n, ok := s.table[ip]
	if !ok {
		return nil
	}
	out := make([]Evidence, len(n.evidence))
	copy(out, n.evidence)
	return out
}

// DrainEvidence returns and clears the forensic records for an IP.
func (s *Switch) DrainEvidence(ip netip.Addr) []Evidence {
	s.mu.Lock()
	defer s.mu.Unlock()

	n, ok := s.table[ip]
	if !ok {
		return nil
	}
	out := n.evidence
	n.evidence = nil
	return out
}

// Events returns a copy of the global audit-event ring.
func (s *Switch) Events() []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}

// LastEvent returns the most recent audit event, if any.
func (s *Switch) LastEvent() (Event, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.events) == 0 {
		return Event{}, false
	}
	return s.events[len(s.events)-1], true
}
