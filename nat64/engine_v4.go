package nat64

func (e *Engine) handleV4TCP(p4 ipv4Packet) {
	l4 := p4.payload
	sport, dport, ok := l4Ports(ProtoTCP, l4)
	if !ok {
		e.drop()
		return
	}

	key := V4SessionKey{
		Src: addr4(p4.src), Dst: addr4(p4.dst),
		Proto: ProtoTCP, SPort: sport, DPort: dport,
	}
	s := e.sessV4[key]

	// RFC 6146 section 3.5.2.2: an unsolicited SYN aimed at a live BIB
	// mapping, with no matching session, enters V4_INIT and is cached for
	// exactly the configured (default 6s) simultaneous-open window.
	if s == nil && l4[tcpFlags]&tcpSYN != 0 && l4[tcpFlags]&tcpACK == 0 {
		b := e.lookupBIBV4(ProtoTCP, addr4(p4.dst), dport)
		if b == nil {
			e.drop() // scanning an unallocated pool port: silent drop
			return
		}
		src6 := e.synthV6(addr4(p4.src)) // remote peer -> synthesized v6 source
		cached := e.translateV4ToV6(p4, sport, b.V6Port, src6.As16(), b.V6Addr.As16())

		v6Key := V6SessionKey{
			Src: src6, Dst: b.V6Addr, Proto: ProtoTCP,
			SPort: sport, DPort: b.V6Port,
		}
		s = e.newSession(ProtoTCP, v6Key, key, StateV4Init, b, cached)
		return
	}

	// Return traffic carries the reversed five tuple (remote -> pool).
	if s == nil {
		s = e.findReverseSession(ProtoTCP, key)
	}
	if s == nil {
		e.drop()
		return
	}

	e.touch(s)
	ev := classifyTCP(l4[tcpFlags])
	e.applyTCPEvent(s, ev, dirV4)

	// applyTCPEvent may have deleted the session on RST; nothing to forward.
	if _, still := e.sessV4[key]; !still {
		return
	}

	b := e.lookupBIBV4(ProtoTCP, addr4(p4.dst), dport) // packet dst == pool, dport == mapped port
	if b == nil {
		e.drop()
		return
	}
	src6 := e.synthV6(addr4(p4.src))
	out := e.translateV4ToV6(p4, sport, b.V6Port, src6.As16(), b.V6Addr.As16())
	e.emitV6(out)
}

func (e *Engine) handleV4UDP(p4 ipv4Packet) {
	l4 := p4.payload
	sport, dport, ok := l4Ports(ProtoUDP, l4)
	if !ok {
		e.drop()
		return
	}

	key := V4SessionKey{
		Src: addr4(p4.src), Dst: addr4(p4.dst),
		Proto: ProtoUDP, SPort: sport, DPort: dport,
	}
	s := e.sessV4[key]
	if s == nil {
		s = e.findReverseSession(ProtoUDP, key)
	}
	if s == nil {
		e.drop() // no session: unsolicited inbound UDP is discarded
		return
	}
	e.touch(s)

	b := e.lookupBIBV4(ProtoUDP, addr4(p4.dst), dport)
	if b == nil {
		e.drop()
		return
	}
	src6 := e.synthV6(addr4(p4.src))
	out := e.translateV4ToV6(p4, sport, b.V6Port, src6.As16(), b.V6Addr.As16())
	e.emitV6(out)
}
