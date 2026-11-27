package nat64

import "encoding/binary"

// ICMP header offsets (shared layout for v4 and v6).
const (
	icmpType     = 0
	icmpCode     = 1
	icmpChecksum = 2
	icmpRest     = 4
	icmpID       = 4
	icmpSeq      = 6
	icmpHeader   = 8
)

const (
	icmp4EchoReply    uint8 = 0
	icmp4DestUnreach  uint8 = 3
	icmp4EchoRequest  uint8 = 8
	icmp4TimeExceeded uint8 = 11
	icmp4ParamProblem uint8 = 12

	icmp6DestUnreach  uint8 = 1
	icmp6PacketTooBig uint8 = 2
	icmp6TimeExceeded uint8 = 3
	icmp6ParamProblem uint8 = 4
	icmp6EchoRequest  uint8 = 128
	icmp6EchoReply    uint8 = 129

	icmpv6Proto = 58
)

// icmp6To4 maps the outer ICMPv6 (type, code) to ICMPv4 (type, code).
func icmp6To4(t, c byte) (uint8, uint8, bool) {
	switch t {
	case icmp6DestUnreach:
		switch c {
		case 0, 2, 3, 5, 6:
			return icmp4DestUnreach, 1, true
		case 1:
			return icmp4DestUnreach, 10, true
		case 4:
			return icmp4DestUnreach, 4, true
		}
	case icmp6PacketTooBig:
		return icmp4DestUnreach, 4, true
	case icmp6TimeExceeded:
		return icmp4TimeExceeded, c, true
	case icmp6ParamProblem:
		if c == 0 {
			return icmp4ParamProblem, 0, true
		}
		return icmp4DestUnreach, 2, true
	}
	return 0, 0, false
}

// icmp4To6 is the reverse mapping.
func icmp4To6(t, c byte) (uint8, uint8, bool) {
	switch t {
	case icmp4DestUnreach:
		switch c {
		case 0, 1, 5, 6, 11, 12:
			return icmp6DestUnreach, 0, true
		case 2:
			return icmp6ParamProblem, 1, true
		case 3:
			return icmp6DestUnreach, 4, true
		case 4:
			return icmp6PacketTooBig, 0, true
		case 9, 10, 13:
			return icmp6DestUnreach, 1, true
		case 7:
			return icmp6DestUnreach, 3, true
		}
	case icmp4TimeExceeded:
		return icmp6TimeExceeded, c, true
	case icmp4ParamProblem:
		return icmp6ParamProblem, 0, true
	case icmp4EchoRequest:
		return icmp6EchoRequest, 0, true
	case icmp4EchoReply:
		return icmp6EchoReply, 0, true
	}
	return 0, 0, false
}

func checksumOffset(proto uint8) int {
	if proto == ProtoTCP {
		return tcpCheck
	}
	return udpCheck
}

func protoFrom6(next uint8) uint8 {
	if next == icmpv6Proto {
		return ProtoICMP
	}
	return next
}

// sendV4Unreachable13 emits the RFC 6146 section 3.5.2.2 response when a
// V4_INIT hold expires unpaired: ICMPv4 Destination Unreachable,
// administratively prohibited (Code 13), quoting the cached SYN.
func (e *Engine) sendV4Unreachable13(s *Session, b *BIBEntry) {
	quoted := s.CachedV4SYN
	if len(quoted) == 0 {
		quoted = e.rebuildMinimalV4(s)
	}
	if len(quoted) > 28 { // IPv4 header + 8 transport bytes per RFC 792
		quoted = quoted[:28]
	}
	// RFC 792: the quoted IP header checksum is not part of the ICMP
	// checksum's meaningful content; clear it before computing.
	if len(quoted) >= ip4HeaderLen {
		quoted = append([]byte(nil), quoted...)
		quoted[ip4Checksum], quoted[ip4Checksum+1] = 0, 0
	}

	msg := make([]byte, icmpHeader+len(quoted))
	msg[icmpType] = icmp4DestUnreach
	msg[icmpCode] = 13
	copy(msg[icmpHeader:], quoted)
	binary.BigEndian.PutUint16(msg[icmpChecksum:], checksum(msg))

	src := s.V4.Dst.As4() // pool address that received the SYN
	dst := s.V4.Src.As4() // IPv4 initiator
	e.emitV4(buildIPv4(0, e.nextIPID(), 1, 64, src, dst, msg))
}

func (e *Engine) rebuildMinimalV4(s *Session) []byte {
	tcpHdr := make([]byte, tcpMinLen)
	binary.BigEndian.PutUint16(tcpHdr[tcpSrcPort:], s.V4.SPort)
	binary.BigEndian.PutUint16(tcpHdr[tcpDstPort:], s.V4.DPort)
	tcpHdr[tcpDataOff] = 5 << 4
	tcpHdr[tcpFlags] = tcpSYN
	binary.BigEndian.PutUint16(tcpHdr[tcpWindow:], 1024)
	return buildIPv4(0, 0, ProtoTCP, 64, s.V4.Src.As4(), s.V4.Dst.As4(), tcpHdr)
}

// ---------------------------------------------------------------------------
// ICMPv6 -> ICMPv4
// ---------------------------------------------------------------------------

func (e *Engine) handleICMPv6(p6 ipv6Packet) {
	icmp := p6.payload
	if len(icmp) < icmpHeader {
		e.drop()
		return
	}
	t := icmp[icmpType]

	if t == icmp6EchoRequest || t == icmp6EchoReply {
		e.handleICMPv6Echo(p6, t)
		return
	}

	t4, c4, ok := icmp6To4(t, icmp[icmpCode])
	if !ok {
		e.drop()
		return
	}

	inner4, s, ok := e.translateInnerV6ToV4(icmp[icmpHeader:])
	if !ok {
		e.drop()
		return
	}

	out := make([]byte, icmpHeader+len(inner4))
	out[icmpType] = t4
	out[icmpCode] = c4
	copy(out[icmpRest:icmpHeader], icmp[icmpRest:icmpHeader])
	if t == icmp6PacketTooBig {
		mtu6 := binary.BigEndian.Uint32(icmp[icmpRest:])
		mtu4 := mtu6 - 20 // RFC 6146 section 5.3.1
		if mtu6 < 20 {
			mtu4 = 0
		}
		binary.BigEndian.PutUint16(out[icmpRest:], 0)
		binary.BigEndian.PutUint16(out[icmpRest+2:], uint16(mtu4))
	}
	copy(out[icmpHeader:], inner4)
	binary.BigEndian.PutUint16(out[icmpChecksum:], checksum(out))

	outerSrc, outerDst := e.outerV4FromV6(p6, s)
	e.emitV4(buildIPv4(0, e.nextIPID(), 1, p6.hop, outerSrc, outerDst, out))
}

func (e *Engine) outerV4FromV6(p6 ipv6Packet, s *Session) (src, dst [4]byte) {
	// RFC 6146: the outer error mirrors the triggering packet's five tuple,
	// so its source is the original IPv4 destination (remote peer) and its
	// destination is the translated source (pool address).
	src = s.V4.Dst.As4()
	dst = s.V4.Src.As4()
	return src, dst
}

// translateInnerV6ToV4 rewrites the triggering packet embedded in an ICMPv6
// error. Because the inner packet may be truncated, transport checksums are
// adjusted incrementally per RFC 1624 rather than recomputed.
func (e *Engine) translateInnerV6ToV4(inner []byte) ([]byte, *Session, bool) {
	p, ok := parseIPv6(inner)
	if !ok {
		return nil, nil, false
	}
	proto := protoFrom6(p.next)
	if proto != ProtoTCP && proto != ProtoUDP && proto != ProtoICMP {
		return nil, nil, false
	}

	var sport, dport uint16
	if proto == ProtoICMP {
		if len(p.payload) < icmpHeader {
			return nil, nil, false
		}
		sport = binary.BigEndian.Uint16(p.payload[icmpID:])
		dport = sport
	} else {
		sport, dport, ok = l4Ports(proto, p.payload)
		if !ok {
			return nil, nil, false
		}
	}

	s, ok := e.sessV6[V6SessionKey{
		Src: addr16(p.src), Dst: addr16(p.dst),
		Proto: proto, SPort: sport, DPort: dport,
	}]
	if !ok {
		return nil, nil, false
	}
	b := e.lookupBIBV6(proto, s.V6.Src, sport)
	if b == nil {
		return nil, nil, false
	}
	remote4, ok := e.extractV4(addr16(p.dst))
	if !ok {
		return nil, nil, false
	}

	l4 := append([]byte(nil), p.payload...)
	// Only the source port is rewritten; the remote destination port is
	// preserved. The delta therefore covers just the source-port word.
	oldWord := uint32(sport) << 16
	newWord := uint32(b.V4Port) << 16
	oldPseudo := pseudoV6(p.src, p.dst, proto, len(l4))
	newPseudo := pseudoV4(b.V4Addr.As4(), remote4.As4(), proto, len(l4))

	if proto == ProtoICMP {
		e.adjustInnerICMP6(l4, oldPseudo, oldWord, newPseudo, newWord)
	} else {
		off := checksumOffset(proto)
		hc := binary.BigEndian.Uint16(l4[off:])
		binary.BigEndian.PutUint16(l4[off:],
			adjustChecksum(hc, oldPseudo+oldWord, newPseudo+newWord))
		setPorts(l4, b.V4Port, dport)
	}

	ipProto := proto
	if proto == ProtoICMP {
		ipProto = 1
	}
	out := buildIPv4(0, 0, ipProto, p.hop, b.V4Addr.As4(), remote4.As4(), l4)
	return out, s, true
}

// adjustInnerICMP6 rewrites an embedded ICMPv6 query into ICMPv4, mapping
// its identifier through the BIB and converting the type byte.
func (e *Engine) adjustInnerICMP6(l4 []byte, oldPseudo, oldWord, newPseudo, newWord uint32) {
	oldType := uint32(l4[icmpType])<<8 | uint32(l4[icmpCode])
	var newType uint32
	switch l4[icmpType] {
	case icmp6EchoRequest:
		newType = uint32(icmp4EchoRequest) << 8
	case icmp6EchoReply:
		newType = uint32(icmp4EchoReply) << 8
	default:
		return
	}
	hc := binary.BigEndian.Uint16(l4[icmpChecksum:])
	cs := adjustChecksum(hc, oldPseudo+oldType+oldWord, newPseudo+newType+newWord)
	binary.BigEndian.PutUint16(l4[icmpChecksum:], cs)
	binary.BigEndian.PutUint16(l4[icmpID:], uint16(newWord>>16))
	if l4[icmpType] == icmp6EchoRequest {
		l4[icmpType] = icmp4EchoRequest
	} else {
		l4[icmpType] = icmp4EchoReply
	}
	l4[icmpCode] = 0
}

// ---------------------------------------------------------------------------
// ICMPv4 -> ICMPv6
// ---------------------------------------------------------------------------

func (e *Engine) handleICMPv4(p4 ipv4Packet) {
	icmp := p4.payload
	if len(icmp) < icmpHeader {
		e.drop()
		return
	}
	t := icmp[icmpType]

	if t == icmp4EchoRequest || t == icmp4EchoReply {
		e.handleICMPv4Echo(p4, t)
		return
	}

	t6, c6, ok := icmp4To6(t, icmp[icmpCode])
	if !ok {
		e.drop()
		return
	}

	inner6, s, ok := e.translateInnerV4ToV6(icmp[icmpHeader:])
	if !ok {
		e.drop()
		return
	}

	out := make([]byte, icmpHeader+len(inner6))
	out[icmpType] = t6
	out[icmpCode] = c6
	copy(out[icmpRest:icmpHeader], icmp[icmpRest:icmpHeader])
	if t == icmp4DestUnreach && icmp[icmpCode] == 4 {
		mtu4 := binary.BigEndian.Uint16(icmp[icmpRest+2:])
		mtu6 := uint32(mtu4) + 20
		if mtu6 < 1280 { // RFC 6146 section 5.3.2 floor
			mtu6 = 1280
		}
		binary.BigEndian.PutUint32(out[icmpRest:], mtu6)
	}
	copy(out[icmpHeader:], inner6)

	osrc, odst := e.outerV6Src(p4, s), e.outerV6Dst(s)
	binary.BigEndian.PutUint16(out[icmpChecksum:],
		transportChecksumV6(osrc, odst, icmpv6Proto, out))
	e.emitV6(buildIPv6(0, icmpv6Proto, p4.ttl, osrc, odst, out))
}

func (e *Engine) outerV6Dst(s *Session) [16]byte {
	// Reverse direction: deliver to the IPv6 client that owned the flow.
	return s.V6.Src.As16()
}

func (e *Engine) outerV6Src(p4 ipv4Packet, s *Session) [16]byte {
	// The outer source embeds the original IPv4 sender (remote peer).
	return e.synthV6(s.V4.Src).As16()
}

func (e *Engine) translateInnerV4ToV6(inner []byte) ([]byte, *Session, bool) {
	p, ok := parseIPv4(inner)
	if !ok {
		return nil, nil, false
	}
	proto := p.proto
	if proto == 1 {
		proto = ProtoICMP
	}
	if proto != ProtoTCP && proto != ProtoUDP && proto != ProtoICMP {
		return nil, nil, false
	}

	var sport, dport uint16
	if proto == ProtoICMP {
		if len(p.payload) < icmpHeader {
			return nil, nil, false
		}
		sport = binary.BigEndian.Uint16(p.payload[icmpID:])
		dport = sport
	} else {
		sport, dport, ok = l4Ports(proto, p.payload)
		if !ok {
			return nil, nil, false
		}
	}

	s, ok := e.sessV4[V4SessionKey{
		Src: addr4(p.src), Dst: addr4(p.dst),
		Proto: proto, SPort: sport, DPort: dport,
	}]
	if !ok {
		return nil, nil, false
	}
	b := e.lookupBIBV4(proto, addr4(p.src), sport)
	if b == nil {
		return nil, nil, false
	}
	dst6 := e.synthV6(addr4(p.dst))

	l4 := append([]byte(nil), p.payload...)
	oldWord := uint32(sport) << 16
	newWord := uint32(b.V6Port) << 16
	oldPseudo := pseudoV4(p.src, p.dst, proto, len(l4))
	newPseudo := pseudoV6(b.V6Addr.As16(), dst6.As16(), proto, len(l4))

	if proto == ProtoICMP {
		e.adjustInnerICMP4(l4, oldPseudo, oldWord, newPseudo, newWord)
	} else {
		off := checksumOffset(proto)
		hc := binary.BigEndian.Uint16(l4[off:])
		binary.BigEndian.PutUint16(l4[off:],
			adjustChecksum(hc, oldPseudo+oldWord, newPseudo+newWord))
		setPorts(l4, b.V6Port, dport)
	}

	next := proto
	if proto == ProtoICMP {
		next = icmpv6Proto
	}
	out := buildIPv6(0, next, p.ttl, b.V6Addr.As16(), dst6.As16(), l4)
	return out, s, true
}

// adjustInnerICMP4 rewrites an embedded ICMPv4 query into ICMPv6.
func (e *Engine) adjustInnerICMP4(l4 []byte, oldPseudo, oldWord, newPseudo, newWord uint32) {
	oldType := uint32(l4[icmpType])<<8 | uint32(l4[icmpCode])
	var newType uint32
	switch l4[icmpType] {
	case icmp4EchoRequest:
		newType = uint32(icmp6EchoRequest) << 8
	case icmp4EchoReply:
		newType = uint32(icmp6EchoReply) << 8
	default:
		return
	}
	hc := binary.BigEndian.Uint16(l4[icmpChecksum:])
	cs := adjustChecksum(hc, oldPseudo+oldType+oldWord, newPseudo+newType+newWord)
	binary.BigEndian.PutUint16(l4[icmpChecksum:], cs)
	binary.BigEndian.PutUint16(l4[icmpID:], uint16(newWord>>16))
	if l4[icmpType] == icmp4EchoRequest {
		l4[icmpType] = icmp6EchoRequest
	} else {
		l4[icmpType] = icmp6EchoReply
	}
	l4[icmpCode] = 0
}

// ---------------------------------------------------------------------------
// ICMP echo query sessions (identifier is modeled as a pseudo-port)
// ---------------------------------------------------------------------------

func (e *Engine) handleICMPv6Echo(p6 ipv6Packet, t byte) {
	icmp := p6.payload
	id := binary.BigEndian.Uint16(icmp[icmpID:])

	b := e.lookupBIBV6(ProtoICMP, addr16(p6.src), id)
	if b == nil {
		var ok bool
		b, ok = e.allocateBIB(ProtoICMP, addr16(p6.src), id)
		if !ok {
			e.drop()
			return
		}
	}

	remote4, ok := e.extractV4(addr16(p6.dst))
	if !ok {
		e.drop()
		return
	}
	b.LastUsed = e.now()

	v6Key := V6SessionKey{Src: addr16(p6.src), Dst: addr16(p6.dst), Proto: ProtoICMP, SPort: id, DPort: id}
	s := e.sessV6[v6Key]
	if s == nil {
		v4Key := V4SessionKey{Src: b.V4Addr, Dst: remote4, Proto: ProtoICMP, SPort: b.V4Port, DPort: id}
		s = e.newSession(ProtoICMP, v6Key, v4Key, StateNone, b, nil)
	} else {
		e.touch(s)
	}

	out := append([]byte(nil), icmp...)
	if t == icmp6EchoRequest {
		out[icmpType] = icmp4EchoRequest
	} else {
		out[icmpType] = icmp4EchoReply
	}
	out[icmpCode] = 0
	binary.BigEndian.PutUint16(out[icmpID:], b.V4Port)
	binary.BigEndian.PutUint16(out[icmpChecksum:], checksum(out))
	e.emitV4(buildIPv4(0, e.nextIPID(), 1, p6.hop, b.V4Addr.As4(), remote4.As4(), out))
}

func (e *Engine) handleICMPv4Echo(p4 ipv4Packet, t byte) {
	icmp := p4.payload
	id := binary.BigEndian.Uint16(icmp[icmpID:])
	b := e.lookupBIBV4(ProtoICMP, addr4(p4.dst), id)
	if b == nil {
		e.drop() // no binding: RFC 6146 drops unsolicited inbound ICMP queries
		return
	}

	v4Key := V4SessionKey{Src: addr4(p4.src), Dst: addr4(p4.dst), Proto: ProtoICMP, SPort: id, DPort: b.V4Port}
	s := e.sessV4[v4Key]
	if s != nil {
		e.touch(s)
	}

	dst6 := e.synthV6(addr4(p4.src))
	out := append([]byte(nil), icmp...)
	if t == icmp4EchoRequest {
		out[icmpType] = icmp6EchoRequest
	} else {
		out[icmpType] = icmp6EchoReply
	}
	out[icmpCode] = 0
	binary.BigEndian.PutUint16(out[icmpID:], b.V6Port)
	binary.BigEndian.PutUint16(out[icmpChecksum:],
		transportChecksumV6(b.V6Addr.As16(), dst6.As16(), icmpv6Proto, out))
	e.emitV6(buildIPv6(0, icmpv6Proto, p4.ttl, b.V6Addr.As16(), dst6.As16(), out))
}
