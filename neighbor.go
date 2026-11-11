package netsec

import (
	"net/netip"
	"time"
)

// neighbor is the per-IP state machine. All methods run under the Switch lock.
type neighbor struct {
	ip netip.Addr

	state State

	// Authorized ticket (control-plane credential).
	hasTicket     bool
	ticketMAC     MAC
	ticketGen     uint64
	ticketExpires time.Time

	// Installed data-plane binding.
	hasBinding  bool
	bindMAC     MAC
	bindPort    PortID
	bindGen     uint64
	confirmedAt time.Time

	// Probe bookkeeping.
	probing       bool
	probes        uint8
	probeDeadline time.Time

	evidence []Evidence
}

func newNeighbor(ip netip.Addr) *neighbor {
	return &neighbor{ip: ip, state: StateEmpty}
}

func (n *neighbor) verdict() (FrameVerdict, PortID) {
	switch n.state {
	case StateReachable:
		return VerdictForward, n.bindPort
	case StateQuarantined:
		return VerdictDrop, 0
	default:
		return VerdictFlood, 0
	}
}

// grant applies a control-plane ticket. Stale generations are refused and can
// never mutate the binding. A newer ticket authorizes a migration: any
// quarantine is lifted and the old binding waits to be replaced by the first
// ticket-allowed ARP confirmation.
func (n *neighbor) grant(t Ticket, now time.Time, seq func() uint64, maxEvidence int) ([]Event, error) {
	if n.hasTicket && t.Generation <= n.ticketGen {
		return []Event{n.mkEvent(seq, now, EventTicketRejected, t.MAC, 0, t.Generation, ReasonNone)},
			ErrStaleTicket
	}

	prevVerdict, _ := n.verdict()
	events := make([]Event, 0, 4)

	n.hasTicket = true
	n.ticketMAC = t.MAC
	n.ticketGen = t.Generation
	if t.TTL > 0 {
		n.ticketExpires = now.Add(t.TTL)
	} else {
		n.ticketExpires = time.Time{}
	}
	n.probing = false
	n.probes = 0
	n.probeDeadline = time.Time{}

	events = append(events, n.mkEvent(seq, now, EventTicketGranted, t.MAC, 0, t.Generation, ReasonNone))

	// A fresh credential always re-opens the neighbor for confirmation. Existing
	// bindings stay in place until the authorized MAC proves itself.
	if n.state == StateQuarantined || n.state == StateEmpty {
		n.state = StateIncomplete
	}

	if v, _ := n.verdict(); v != prevVerdict {
		events = append(events, n.mkEvent(seq, now, EventForwardingChanged, n.bindMAC, n.bindPort, n.ticketGen, ReasonNone))
	}
	return events, nil
}

// revoke removes the active ticket. Bindings are retained as stale forensic
// state but can no longer be confirmed; quarantine is never lifted by revoke.
func (n *neighbor) revoke(now time.Time, seq func() uint64) ([]Event, error) {
	if !n.hasTicket && !n.hasBinding && n.state == StateEmpty {
		return nil, ErrUnknownNeighbor
	}
	prevVerdict, _ := n.verdict()
	events := []Event{n.mkEvent(seq, now, EventTicketRevoked, n.ticketMAC, 0, n.ticketGen, ReasonNone)}

	n.hasTicket = false
	n.ticketMAC = ""
	n.ticketExpires = time.Time{}
	n.probing = false
	n.probes = 0

	if n.state != StateQuarantined {
		if n.hasBinding {
			n.state = StateStale
		} else {
			n.state = StateEmpty
		}
	}
	if v, _ := n.verdict(); v != prevVerdict {
		events = append(events, n.mkEvent(seq, now, EventForwardingChanged, n.bindMAC, n.bindPort, n.bindGen, ReasonNone))
	}
	return events, nil
}

func (n *neighbor) expireTicketIfDue(now time.Time, seq func() uint64) []Event {
	if !n.hasTicket || n.ticketExpires.IsZero() || now.Before(n.ticketExpires) {
		return nil
	}
	prevVerdict, _ := n.verdict()
	events := []Event{n.mkEvent(seq, now, EventTicketExpired, n.ticketMAC, 0, n.ticketGen, ReasonNone)}

	n.hasTicket = false
	n.ticketMAC = ""
	n.ticketExpires = time.Time{}
	n.probing = false
	n.probes = 0

	if n.state != StateQuarantined {
		if n.hasBinding {
			n.state = StateStale
		} else {
			n.state = StateEmpty
		}
	}
	if v, _ := n.verdict(); v != prevVerdict {
		events = append(events, n.mkEvent(seq, now, EventForwardingChanged, n.bindMAC, n.bindPort, n.bindGen, ReasonNone))
	}
	return events
}

// arpObservation is a data-plane ARP claim: senderMAC claims senderIP on port.
type arpObservation struct {
	mac  MAC
	port PortID
}

// observeARP is the data-plane entry point. Only the MAC named by the active
// ticket can confirm or move a binding. Every other claim quarantines the IP
// and retains evidence; late confirmations from older generations are treated
// as conflicts and never overwrite the newer binding.
func (n *neighbor) observeARP(obs arpObservation, now time.Time, seq func() uint64, maxEvidence int) (Reason, []Event) {
	events := n.expireTicketIfDue(now, seq)

	// While quarantined, nothing the data plane says is trusted.
	if n.state == StateQuarantined {
		kind := n.classify(obs)
		n.addEvidence(kind, obs, now, seq, maxEvidence)
		events = append(events, n.mkEvent(seq, now, EventARPRejected, obs.mac, obs.port, n.ticketGen, ReasonQuarantined))
		return ReasonQuarantined, events
	}

	if !n.hasTicket {
		kind := n.classify(obs)
		events = append(events, n.quarantine(kind, obs, now, seq, maxEvidence, ReasonNoTicket)...)
		return ReasonNoTicket, events
	}

	if obs.mac != n.ticketMAC {
		kind := n.classify(obs)
		events = append(events, n.quarantine(kind, obs, now, seq, maxEvidence, ReasonMACNotAllowed)...)
		return ReasonMACNotAllowed, events
	}

	// Authorized confirmation.
	n.probing = false
	n.probes = 0
	n.probeDeadline = time.Time{}
	prevVerdict, prevPort := n.verdict()

	if !n.hasBinding || n.bindMAC != obs.mac {
		n.hasBinding = true
		n.bindMAC = obs.mac
		n.bindPort = obs.port
		n.bindGen = n.ticketGen
		n.confirmedAt = now
		n.state = StateReachable
		events = append(events, n.mkEvent(seq, now, EventBindingInstalled, obs.mac, obs.port, n.ticketGen, ReasonNone))
	} else {
		n.bindPort = obs.port
		// Refresh the binding generation: a confirmation always proves the
		// current ticket, never the generation it was first installed under.
		n.bindGen = n.ticketGen
		n.confirmedAt = now
		n.state = StateReachable
		events = append(events, n.mkEvent(seq, now, EventBindingConfirmed, obs.mac, obs.port, n.ticketGen, ReasonNone))
	}

	if v, port := n.verdict(); v != prevVerdict || port != prevPort {
		events = append(events, n.mkEvent(seq, now, EventForwardingChanged, obs.mac, obs.port, n.ticketGen, ReasonNone))
	}
	return ReasonNone, events
}

// classify maps an unauthorized ARP claim to an evidence kind.
func (n *neighbor) classify(obs arpObservation) EvidenceKind {
	switch {
	case n.hasBinding && obs.mac == n.bindMAC && n.hasTicket && n.ticketGen > n.bindGen:
		// The old generation's MAC is confirming after a newer ticket won.
		return EvidenceLateConfirm
	case n.hasBinding && obs.mac != n.bindMAC:
		return EvidenceMACConflict
	default:
		return EvidenceUnauthorizedARP
	}
}

func (n *neighbor) quarantine(kind EvidenceKind, obs arpObservation, now time.Time, seq func() uint64, maxEvidence int, reason Reason) []Event {
	n.addEvidence(kind, obs, now, seq, maxEvidence)
	events := []Event{n.mkEvent(seq, now, EventARPRejected, obs.mac, obs.port, n.ticketGen, reason)}

	prevVerdict, _ := n.verdict()
	n.state = StateQuarantined
	n.probing = false
	n.probeDeadline = time.Time{}

	events = append(events, n.mkEvent(seq, now, EventQuarantined, obs.mac, obs.port, n.ticketGen, reason))
	if v, _ := n.verdict(); v != prevVerdict {
		events = append(events, n.mkEvent(seq, now, EventForwardingChanged, obs.mac, obs.port, n.ticketGen, reason))
	}
	return events
}

func (n *neighbor) addEvidence(kind EvidenceKind, obs arpObservation, now time.Time, seq func() uint64, maxEvidence int) {
	e := Evidence{
		Seq:          seq(),
		At:           now,
		IP:           n.ip,
		Kind:         kind,
		ObservedMAC:  obs.mac,
		ObservedPort: obs.port,
		AllowedMAC:   n.ticketMAC,
		TicketGen:    n.ticketGen,
		BindingGen:   n.bindGen,
	}
	n.evidence = append(n.evidence, e)
	if maxEvidence > 0 && len(n.evidence) > maxEvidence {
		n.evidence = n.evidence[len(n.evidence)-maxEvidence:]
	}
}

// startResolution begins ARP resolution for an authorized IP without a
// confirmed binding.
func (n *neighbor) startResolution(now time.Time, interval time.Duration, seq func() uint64) ([]Event, error) {
	if n.state == StateQuarantined {
		return nil, ErrQuarantined
	}
	if !n.hasTicket {
		return nil, ErrNoTicket
	}
	if n.hasBinding {
		return nil, ErrAlreadyBound
	}
	if n.probing {
		return nil, ErrAlreadyBound
	}
	n.state = StateIncomplete
	n.probing = true
	n.probes = 1
	n.probeDeadline = now.Add(interval)
	return []Event{n.mkEvent(seq, now, EventProbeStarted, n.ticketMAC, 0, n.ticketGen, ReasonNone)}, nil
}

// revalidate re-probes a stale but still authorized binding.
func (n *neighbor) revalidate(now time.Time, interval time.Duration, seq func() uint64) ([]Event, error) {
	if n.state == StateQuarantined {
		return nil, ErrQuarantined
	}
	if !n.hasTicket {
		return nil, ErrNoTicket
	}
	if n.state != StateStale {
		return nil, ErrNotStale
	}
	n.state = StateProbe
	n.probing = true
	n.probes = 1
	n.probeDeadline = now.Add(interval)
	return []Event{n.mkEvent(seq, now, EventProbeStarted, n.bindMAC, n.bindPort, n.ticketGen, ReasonNone)}, nil
}

// tick drives reachability aging and probe timeouts using the host's clock.
func (n *neighbor) tick(now time.Time, cfg Config, seq func() uint64) []Event {
	events := n.expireTicketIfDue(now, seq)

	switch n.state {
	case StateReachable:
		if !n.confirmedAt.IsZero() && now.Sub(n.confirmedAt) >= cfg.ReachableTTL {
			prevVerdict, _ := n.verdict()
			n.state = StateStale
			if v, _ := n.verdict(); v != prevVerdict {
				events = append(events, n.mkEvent(seq, now, EventForwardingChanged, n.bindMAC, n.bindPort, n.bindGen, ReasonNone))
			}
		}
	case StateIncomplete:
		events = append(events, n.tickProbe(now, cfg, seq, true)...)
	case StateProbe:
		events = append(events, n.tickProbe(now, cfg, seq, false)...)
	}
	return events
}

func (n *neighbor) tickProbe(now time.Time, cfg Config, seq func() uint64, incomplete bool) []Event {
	if !n.probing || now.Before(n.probeDeadline) {
		return nil
	}
	events := []Event{n.mkEvent(seq, now, EventProbeTimeout, n.bindMAC, n.bindPort, n.ticketGen, ReasonNone)}

	if n.probes >= cfg.MaxProbes {
		// Exhausted: give up. A failed revalidation removes the binding; a
		// failed first resolution leaves the neighbor incomplete.
		n.probing = false
		n.probeDeadline = time.Time{}
		n.probes = 0
		if !incomplete {
			prevVerdict, _ := n.verdict()
			n.hasBinding = false
			n.bindMAC = ""
			n.bindPort = 0
			n.bindGen = 0
			n.confirmedAt = time.Time{}
			n.state = StateIncomplete
			if v, _ := n.verdict(); v != prevVerdict {
				events = append(events, n.mkEvent(seq, now, EventForwardingChanged, "", 0, n.ticketGen, ReasonNone))
			}
		}
		events = append(events, n.mkEvent(seq, now, EventResolutionFailed, n.ticketMAC, 0, n.ticketGen, ReasonNone))
		return events
	}

	n.probes++
	n.probeDeadline = now.Add(cfg.ProbeInterval)
	return events
}

func (n *neighbor) mkEvent(seq func() uint64, now time.Time, typ EventType, mac MAC, port PortID, gen uint64, reason Reason) Event {
	return Event{
		Seq:    seq(),
		At:     now,
		Type:   typ,
		IP:     n.ip,
		MAC:    mac,
		Port:   port,
		Gen:    gen,
		State:  n.state,
		Reason: reason,
	}
}
