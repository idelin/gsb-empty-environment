package nat64

import "net/netip"

// HandleV6 processes an inbound IPv6 packet and emits zero or more
// translated IPv4 packets through Config.OnV4.
func (e *Engine) HandleV6(packet []byte) {
	e.stats.PacketsV6In++
	p6, ok := parseIPv6(packet)
	if !ok {
		e.drop()
		return
	}

	switch p6.next {
	case ProtoTCP:
		e.handleV6TCP(p6)
	case ProtoUDP:
		e.handleV6UDP(p6)
	case icmpv6Proto:
		e.handleICMPv6(p6)
	default:
		e.drop()
	}
}

// HandleV4 processes an inbound IPv4 packet and emits zero or more
// translated IPv6 packets (or locally generated ICMPv4 errors) through the
// configured callbacks.
func (e *Engine) HandleV4(packet []byte) {
	e.stats.PacketsV4In++
	p4, ok := parseIPv4(packet)
	if !ok {
		e.drop()
		return
	}

	switch p4.proto {
	case ProtoTCP:
		e.handleV4TCP(p4)
	case ProtoUDP:
		e.handleV4UDP(p4)
	case 1:
		e.handleICMPv4(p4)
	default:
		e.drop()
	}
}

// ---------------------------------------------------------------------------
// IPv6 side
// ---------------------------------------------------------------------------

func (e *Engine) handleV6TCP(p6 ipv6Packet) {
	l4 := p6.payload
	sport, dport, ok := l4Ports(ProtoTCP, l4)
	if !ok {
		e.drop()
		return
	}
	src6, dst6 := addr16(p6.src), addr16(p6.dst)

	// Simultaneous-open pairing: an IPv6 SYN that matches a cached V4_INIT
	// hold for the same remote endpoint completes the handshake.
	if l4[tcpFlags]&tcpSYN != 0 && l4[tcpFlags]&tcpACK == 0 {
		remote4, extractable := e.extractV4(dst6)
		if extractable {
			if hold := e.findV4InitForBIB(src6, sport, remote4, dport); hold != nil {
				e.pairV4Init(hold, p6, sport, dport, remote4)
				return
			}
		}
	}

	key := V6SessionKey{Src: src6, Dst: dst6, Proto: ProtoTCP, SPort: sport, DPort: dport}
	s := e.sessV6[key]

	if s == nil && l4[tcpFlags]&tcpSYN != 0 && l4[tcpFlags]&tcpACK == 0 {
		s = e.createOutboundSession(ProtoTCP, key, p6, sport, dport)
		if s == nil {
			return
		}
	}
	if s == nil {
		e.drop()
		return
	}

	e.touch(s)
	e.applyTCPEvent(s, classifyTCP(l4[tcpFlags]), dirV6)

	b := e.lookupBIBV6(ProtoTCP, src6, sport)
	if b == nil {
		e.drop()
		return
	}
	remote4, ok := e.extractV4(dst6)
	if !ok {
		e.drop()
		return
	}
	out := e.translateV6ToV4(p6, b.V4Port, dport, b.V4Addr.As4(), remote4.As4())
	e.emitV4(out)
}

func (e *Engine) handleV6UDP(p6 ipv6Packet) {
	l4 := p6.payload
	sport, dport, ok := l4Ports(ProtoUDP, l4)
	if !ok {
		e.drop()
		return
	}
	src6, dst6 := addr16(p6.src), addr16(p6.dst)
	remote4, ok := e.extractV4(dst6)
	if !ok {
		e.drop()
		return
	}

	key := V6SessionKey{Src: src6, Dst: dst6, Proto: ProtoUDP, SPort: sport, DPort: dport}
	s := e.sessV6[key]
	b := e.lookupBIBV6(ProtoUDP, src6, sport)
	if s == nil {
		if b == nil {
			var ok bool
			b, ok = e.allocateBIB(ProtoUDP, src6, sport)
			if !ok {
				e.drop()
				return
			}
		}
		v4Key := V4SessionKey{Src: b.V4Addr, Dst: remote4, Proto: ProtoUDP, SPort: b.V4Port, DPort: dport}
		if _, collision := e.sessV4[v4Key]; collision && e.sessV4[v4Key].V6 != key {
			e.drop() // distinct EIM flow cannot share a five tuple
			return
		}
		s = e.newSession(ProtoUDP, key, v4Key, StateNone, b, nil)
	}
	e.touch(s)
	b.LastUsed = e.now()

	out := e.translateV6ToV4(p6, b.V4Port, dport, b.V4Addr.As4(), remote4.As4())
	e.emitV4(out)
}

// createOutboundSession allocates the EIM binding (if needed) and the
// initial V6_INIT TCP session for a fresh IPv6 SYN.
func (e *Engine) createOutboundSession(proto uint8, v6Key V6SessionKey, p6 ipv6Packet, sport, dport uint16) *Session {
	b := e.lookupBIBV6(proto, v6Key.Src, sport)
	if b == nil {
		var ok bool
		b, ok = e.allocateBIB(proto, v6Key.Src, sport)
		if !ok {
			e.drop()
			return nil
		}
	}
	remote4, ok := e.extractV4(v6Key.Dst)
	if !ok {
		e.drop()
		return nil
	}
	v4Key := V4SessionKey{Src: b.V4Addr, Dst: remote4, Proto: proto, SPort: b.V4Port, DPort: dport}
	if existing := e.sessV4[v4Key]; existing != nil && existing.V6 != v6Key {
		e.drop()
		return nil
	}
	return e.newSession(proto, v6Key, v4Key, StateV6Init, b, nil)
}

// applyTCPEvent drives the six-state table and closes the session when the
// table returns closeSession (RST in any state).
func (e *Engine) applyTCPEvent(s *Session, ev tcpEvent, dir direction) {
	var next TCPState
	if ev == evFIN {
		next = tcpFinTransition(s.State, dir)
	} else {
		next = tcpTransition(s.State, ev)
	}
	if next == closeSession {
		e.closeSession(s, "rst")
		return
	}
	s.State = next
	s.ExpiresAt = e.sessionDeadline(s, e.now())
}

// findV4InitForBIB locates the cached unsolicited-SYN hold whose BIB
// belongs to this IPv6 endpoint and whose remote matches the new SYN.
func (e *Engine) findV4InitForBIB(src6 netip.Addr, sport uint16, remote4 netip.Addr, dport uint16) *Session {
	b := e.lookupBIBV6(ProtoTCP, src6, sport)
	if b == nil {
		return nil
	}
	// The hold stores the on-wire IPv4 tuple: the remote peer's SYN came
	// from (remote4, dport) toward the pool mapping (b.V4Addr, b.V4Port).
	v4Key := V4SessionKey{
		Src: remote4, Dst: b.V4Addr, Proto: ProtoTCP, SPort: dport, DPort: b.V4Port,
	}
	s := e.sessV4[v4Key]
	if s != nil && s.State == StateV4Init {
		return s
	}
	return nil
}

// pairV4Init performs the RFC 6146 simultaneous-open pairing: the hold
// moves to ESTABLISHED, both SYNs are emitted and bidirectional traffic
// flows.
func (e *Engine) pairV4Init(hold *Session, p6 ipv6Packet, sport, dport uint16, remote4 netip.Addr) {
	b := e.lookupBIBV6(ProtoTCP, addr16(p6.src), sport)
	if b == nil {
		b = e.lookupBIBV4(ProtoTCP, hold.V4.Dst, hold.V4.DPort)
	}

	// Re-key the session from the temporary V4_INIT key to the real pair.
	delete(e.sessV4, hold.V4)
	hold.V6 = V6SessionKey{Src: addr16(p6.src), Dst: addr16(p6.dst), Proto: ProtoTCP, SPort: sport, DPort: dport}
	hold.V4 = V4SessionKey{Src: b.V4Addr, Dst: remote4, Proto: ProtoTCP, SPort: b.V4Port, DPort: dport}
	e.sessV6[hold.V6] = hold
	e.sessV4[hold.V4] = hold
	hold.State = StateV6Established
	cached := hold.CachedV4SYN
	hold.CachedV4SYN = nil
	e.touch(hold)

	// Deliver the previously cached IPv4 SYN toward IPv6 first, then the
	// fresh IPv6 SYN toward IPv4.
	if len(cached) > 0 {
		e.emitV6(cached)
	}
	out := e.translateV6ToV4(p6, b.V4Port, dport, b.V4Addr.As4(), remote4.As4())
	e.emitV4(out)
}
