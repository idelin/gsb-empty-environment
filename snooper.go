package dhcpsnoop

import "time"

// DefaultTransactionTTL is how long a tracked client transaction
// (DISCOVER/REQUEST/INFORM) is remembered for reply validation.
const DefaultTransactionTTL = 10 * time.Minute

// transaction is the per-XID client state used to validate server replies.
type transaction struct {
	mac    MAC
	port   Port
	kind   DHCPType
	expiry time.Time
}

// Snooper is a DHCP snooping / dynamic ARP inspection / IP source guard
// component for an access switch. It is not safe for concurrent use; the
// virtual switch drives it sequentially, one packet at a time.
type Snooper struct {
	clock   Clock
	trusted map[Port]struct{}

	// bindings is the single binding table shared by DHCP snooping,
	// ARP inspection and IP source guard, keyed by leased IP.
	bindings map[IPv4]Binding

	// transactions tracks outstanding client messages by XID.
	transactions map[uint32]transaction

	txTTL time.Duration
}

// New creates a Snooper. trustedPorts are the uplink ports connected to
// legitimate DHCP servers; every other port is an untrusted access port.
// At least one trusted port is required.
func New(clock Clock, trustedPorts ...Port) *Snooper {
	if len(trustedPorts) == 0 {
		panic("dhcpsnoop: at least one trusted (uplink) port is required")
	}
	trusted := make(map[Port]struct{}, len(trustedPorts))
	for _, p := range trustedPorts {
		trusted[p] = struct{}{}
	}
	return &Snooper{
		clock:        clock,
		trusted:      trusted,
		bindings:     make(map[IPv4]Binding),
		transactions: make(map[uint32]transaction),
		txTTL:        DefaultTransactionTTL,
	}
}

// SetTransactionTTL overrides the lifetime of tracked transactions. It
// must be called before any packet is processed.
func (s *Snooper) SetTransactionTTL(d time.Duration) { s.txTTL = d }

func (s *Snooper) isTrusted(p Port) bool {
	_, ok := s.trusted[p]
	return ok
}

func isServerMessage(t DHCPType) bool {
	return t == DHCPOffer || t == DHCPAck || t == DHCPNak
}

func isClientMessage(t DHCPType) bool {
	switch t {
	case DHCPDiscover, DHCPRequest, DHCPDecline, DHCPRelease, DHCPInform:
		return true
	default:
		return false
	}
}

// HandleDHCP inspects one DHCP packet and returns its forwarding verdict.
// State changes (binding creation/removal, transaction tracking) happen
// only for accepted, well-formed packets.
func (s *Snooper) HandleDHCP(pkt DHCPPacket) Result {
	now := s.clock.Now()
	s.sweepExpired(now)

	if pkt.Type == 0 || pkt.Type > DHCPInform || pkt.Chaddr.IsZero() {
		return drop(ReasonInvalidMessage)
	}

	// Only trusted uplink ports may carry server-to-client messages;
	// a forged OFFER/ACK/NAK arriving on an access port is dropped.
	if isServerMessage(pkt.Type) && !s.isTrusted(pkt.InPort) {
		return drop(ReasonUntrustedServerReply)
	}

	// DHCP from the trusted side (e.g. relayed traffic) is not policed.
	if s.isTrusted(pkt.InPort) && isClientMessage(pkt.Type) {
		return allow(ReasonTrustedBypass)
	}

	switch pkt.Type {
	case DHCPDiscover, DHCPRequest, DHCPInform:
		s.transactions[pkt.XID] = transaction{
			mac:    pkt.Chaddr,
			port:   pkt.InPort,
			kind:   pkt.Type,
			expiry: now.Add(s.txTTL),
		}
		return allow(ReasonOK)

	case DHCPOffer:
		return s.validateReply(pkt)

	case DHCPNak:
		res := s.validateReply(pkt)
		if res.Allowed() {
			delete(s.transactions, pkt.XID)
		}
		return res

	case DHCPAck:
		return s.handleAck(pkt, now)

	case DHCPRelease, DHCPDecline:
		return s.handleReleaseOrDecline(pkt)

	default:
		return drop(ReasonInvalidMessage)
	}
}

// validateReply checks a trusted OFFER or NAK against the tracked client
// transaction. Neither message modifies the binding table.
func (s *Snooper) validateReply(pkt DHCPPacket) Result {
	tx, ok := s.transactions[pkt.XID]
	if !ok {
		return drop(ReasonTransactionUnknown)
	}
	if tx.mac != pkt.Chaddr {
		return drop(ReasonClientIDMismatch)
	}
	return allow(ReasonOK)
}

func (s *Snooper) handleAck(pkt DHCPPacket, now time.Time) Result {
	tx, txKnown := s.transactions[pkt.XID]

	// Fast path: a valid, tracked transaction.
	if txKnown {
		if tx.mac != pkt.Chaddr {
			return drop(ReasonClientIDMismatch)
		}
		// INFORM ACKs carry neither leased address nor lease time and
		// never create a binding.
		if tx.kind == DHCPInform {
			if !pkt.Yiaddr.IsZero() || pkt.Lease != 0 {
				return drop(ReasonInvalidMessage)
			}
			delete(s.transactions, pkt.XID)
			return allow(ReasonOK)
		}
	}

	if pkt.Yiaddr.IsZero() || pkt.Lease <= 0 {
		return drop(ReasonNoLease)
	}

	// A duplicate ACK after the transaction aged out is still accepted
	// when it refreshes an existing binding for the same client; it must
	// never hijack another client's lease.
	existing, conflict := s.bindings[pkt.Yiaddr]
	if conflict {
		if existing.MAC != pkt.Chaddr {
			return drop(ReasonBindingConflict)
		}
		if txKnown && tx.port != existing.Port {
			// Deterministic port migration: the latest transaction
			// wins and the binding moves to the new access port.
			existing.Port = tx.port
		}
		existing.Expiry = now.Add(pkt.Lease)
		s.bindings[pkt.Yiaddr] = existing
		return allow(ReasonOK)
	}

	// No existing binding for the offered IP. Without a tracked
	// transaction the ACK cannot be attributed to a local client.
	if !txKnown {
		return drop(ReasonTransactionUnknown)
	}

	s.bindings[pkt.Yiaddr] = Binding{
		IP:     pkt.Yiaddr,
		MAC:    pkt.Chaddr,
		Port:   tx.port,
		Expiry: now.Add(pkt.Lease),
	}
	return allow(ReasonOK)
}

func (s *Snooper) handleReleaseOrDecline(pkt DHCPPacket) Result {
	ip := pkt.Ciaddr
	if ip.IsZero() {
		return drop(ReasonInvalidMessage)
	}
	b, ok := s.bindings[ip]
	if !ok {
		return drop(ReasonReleaseNoMatch)
	}
	if b.MAC != pkt.Chaddr {
		return drop(ReasonClientIDMismatch)
	}
	if b.Port != pkt.InPort {
		return drop(ReasonPortMismatch)
	}
	delete(s.bindings, ip)

	// The client's outstanding transaction, if any, is gone as well.
	for xid, tx := range s.transactions {
		if tx.mac == pkt.Chaddr {
			delete(s.transactions, xid)
		}
	}
	return allow(ReasonOK)
}

// HandleARP performs dynamic ARP inspection using the binding table.
// ARP probes (sender IP 0.0.0.0, RFC 5227) are always allowed and never
// create a binding. Trusted-port ARP traffic bypasses inspection.
func (s *Snooper) HandleARP(pkt ARPPacket) Result {
	now := s.clock.Now()
	s.sweepExpired(now)

	if s.isTrusted(pkt.InPort) {
		return allow(ReasonTrustedBypass)
	}
	if pkt.SenderMAC.IsZero() {
		return drop(ReasonInvalidMessage)
	}
	if pkt.SenderIP.IsZero() {
		return allow(ReasonArpProbe)
	}
	return s.matchBinding(pkt.SenderIP, pkt.SenderMAC, pkt.InPort)
}

// HandleIP performs IP source guard for a non-ARP IPv4 packet.
func (s *Snooper) HandleIP(pkt IPPacket) Result {
	now := s.clock.Now()
	s.sweepExpired(now)

	if s.isTrusted(pkt.InPort) {
		return allow(ReasonTrustedBypass)
	}
	if pkt.SrcIP.IsZero() || pkt.SrcMAC.IsZero() {
		return drop(ReasonInvalidMessage)
	}
	return s.matchBinding(pkt.SrcIP, pkt.SrcMAC, pkt.InPort)
}

// matchBinding is the shared lookup used by both ARP inspection and IP
// source guard. The verdict distinguishes the three failure modes so the
// outcome of every packet is unambiguous.
func (s *Snooper) matchBinding(ip IPv4, mac MAC, port Port) Result {
	b, ok := s.bindings[ip]
	if !ok {
		return drop(ReasonNoBinding)
	}
	if b.Port != port {
		return drop(ReasonPortMismatch)
	}
	if b.MAC != mac {
		return drop(ReasonMacMismatch)
	}
	return allow(ReasonOK)
}

// Sweep removes expired lease bindings and stale transactions. It is
// invoked automatically on every packet, and may also be driven directly
// by the virtual switch's timer task.
func (s *Snooper) Sweep() int {
	return s.sweepExpired(s.clock.Now())
}

func (s *Snooper) sweepExpired(now time.Time) int {
	removed := 0
	for ip, b := range s.bindings {
		if !b.ActiveAt(now) {
			delete(s.bindings, ip)
			removed++
		}
	}
	for xid, tx := range s.transactions {
		if !now.Before(tx.expiry) {
			delete(s.transactions, xid)
		}
	}
	return removed
}

// Binding returns the binding for an IP and whether it exists. Entries
// that reached Expiry are reaped on the next packet or by Sweep; this
// accessor itself is a pure map read and does not advance state.
func (s *Snooper) Binding(ip IPv4) (Binding, bool) {
	b, ok := s.bindings[ip]
	return b, ok
}

// Bindings returns a snapshot copy of the binding table.
func (s *Snooper) Bindings() []Binding {
	out := make([]Binding, 0, len(s.bindings))
	for _, b := range s.bindings {
		out = append(out, b)
	}
	return out
}

// BindingCount returns the number of entries in the binding table.
func (s *Snooper) BindingCount() int { return len(s.bindings) }
