package nat64

import "encoding/binary"

type direction int

const (
	dirV6 direction = iota
	dirV4
)

// IPv4 header layout.
const (
	ip4VersionIHL = 0
	ip4TOS        = 1
	ip4TotalLen   = 2
	ip4ID         = 4
	ip4FlagsFrag  = 6
	ip4TTL        = 8
	ip4Protocol   = 9
	ip4Checksum   = 10
	ip4Src        = 12
	ip4Dst        = 16
	ip4HeaderLen  = 20
)

// IPv6 header layout.
const (
	ip6VersionTC = 0
	ip6Flow      = 1
	ip6Payload   = 4
	ip6NextHdr   = 6
	ip6HopLimit  = 7
	ip6Src       = 8
	ip6Dst       = 24
	ip6HeaderLen = 40
)

// Transport header offsets.
const (
	tcpSrcPort = 0
	tcpDstPort = 2
	tcpSeq     = 4
	tcpAck     = 8
	tcpDataOff = 12
	tcpFlags   = 13
	tcpWindow  = 14
	tcpCheck   = 16
	tcpUrg     = 18
	tcpMinLen  = 20

	udpSrcPort = 0
	udpDstPort = 2
	udpLength  = 4
	udpCheck   = 6
	udpMinLen  = 8
)

type ipv6Packet struct {
	src     [16]byte
	dst     [16]byte
	next    uint8
	hop     uint8
	tc      byte
	payload []byte
}

type ipv4Packet struct {
	src      [4]byte
	dst      [4]byte
	proto    uint8
	ttl      byte
	tos      byte
	id       uint16
	fragOff  uint16
	moreFrag bool
	payload  []byte
}

// parseIPv6 requires a fixed header without extension headers (next header
// must be TCP, UDP or ICMPv6).
func parseIPv6(pkt []byte) (ipv6Packet, bool) {
	if len(pkt) < ip6HeaderLen || pkt[0]>>4 != 6 {
		return ipv6Packet{}, false
	}
	p := ipv6Packet{
		next:    pkt[ip6NextHdr],
		hop:     pkt[ip6HopLimit],
		tc:      (pkt[ip6VersionTC]&0x0f)<<4 | pkt[ip6Flow]>>4,
		payload: pkt[ip6HeaderLen:],
	}
	copy(p.src[:], pkt[ip6Src:ip6Src+16])
	copy(p.dst[:], pkt[ip6Dst:ip6Dst+16])
	return p, true
}

// parseIPv4 supports only the base 20-byte header (IHL == 5) and rejects
// fragments, matching the no-reassembly scope of RFC 6146.
func parseIPv4(pkt []byte) (ipv4Packet, bool) {
	if len(pkt) < ip4HeaderLen || pkt[ip4VersionIHL]>>4 != 4 {
		return ipv4Packet{}, false
	}
	ihl := int(pkt[ip4VersionIHL]&0x0f) * 4
	if ihl != ip4HeaderLen {
		return ipv4Packet{}, false
	}
	total := int(binary.BigEndian.Uint16(pkt[ip4TotalLen:]))
	if total < ip4HeaderLen || total > len(pkt) {
		return ipv4Packet{}, false
	}
	frag := binary.BigEndian.Uint16(pkt[ip4FlagsFrag:])
	if frag&0x1fff != 0 {
		return ipv4Packet{}, false // fragmented or MF set
	}
	p := ipv4Packet{
		proto:   pkt[ip4Protocol],
		ttl:     pkt[ip4TTL],
		tos:     pkt[ip4TOS],
		id:      binary.BigEndian.Uint16(pkt[ip4ID:]),
		payload: pkt[ip4HeaderLen:total],
	}
	copy(p.src[:], pkt[ip4Src:ip4Src+4])
	copy(p.dst[:], pkt[ip4Dst:ip4Dst+4])
	return p, true
}

func l4Ports(proto uint8, l4 []byte) (src, dst uint16, ok bool) {
	switch proto {
	case ProtoTCP:
		if len(l4) < tcpMinLen {
			return 0, 0, false
		}
	case ProtoUDP:
		if len(l4) < udpMinLen {
			return 0, 0, false
		}
	default:
		return 0, 0, false
	}
	return binary.BigEndian.Uint16(l4[0:2]), binary.BigEndian.Uint16(l4[2:4]), true
}

func setPorts(l4 []byte, src, dst uint16) {
	binary.BigEndian.PutUint16(l4[0:2], src)
	binary.BigEndian.PutUint16(l4[2:4], dst)
}

// recomputeL4 rewrites the transport checksum after address/port changes.
func recomputeL4(proto uint8, l4 []byte, v4src, v4dst [4]byte, v6src, v6dst [16]byte, dir direction) {
	switch proto {
	case ProtoTCP:
		l4[tcpCheck], l4[tcpCheck+1] = 0, 0
		var cs uint16
		if dir == dirV6 {
			cs = transportChecksumV6(v6src, v6dst, ProtoTCP, l4)
		} else {
			cs = transportChecksumV4(v4src, v4dst, ProtoTCP, l4)
		}
		binary.BigEndian.PutUint16(l4[tcpCheck:], cs)
	case ProtoUDP:
		l4[udpCheck], l4[udpCheck+1] = 0, 0
		var cs uint16
		if dir == dirV6 {
			cs = transportChecksumV6(v6src, v6dst, ProtoUDP, l4)
		} else {
			cs = transportChecksumV4(v4src, v4dst, ProtoUDP, l4)
		}
		if cs == 0 {
			cs = 0xffff // UDP over IPv4 transmits 0xffff instead of 0
		}
		binary.BigEndian.PutUint16(l4[udpCheck:], cs)
	}
}

// buildIPv4 assembles an IPv4 packet from a complete payload slice.
func buildIPv4(tos byte, id uint16, proto uint8, ttl byte, src, dst [4]byte, payload []byte) []byte {
	pkt := make([]byte, ip4HeaderLen+len(payload))
	pkt[ip4VersionIHL] = 0x45
	pkt[ip4TOS] = tos
	binary.BigEndian.PutUint16(pkt[ip4TotalLen:], uint16(len(pkt)))
	binary.BigEndian.PutUint16(pkt[ip4ID:], id)
	// DF=0, fragment offset 0: PTB-driven fragmentation remains possible.
	pkt[ip4TTL] = decrementHop(ttl)
	pkt[ip4Protocol] = proto
	copy(pkt[ip4Src:ip4Src+4], src[:])
	copy(pkt[ip4Dst:ip4Dst+4], dst[:])
	binary.BigEndian.PutUint16(pkt[ip4Checksum:], checksum(pkt[:ip4HeaderLen]))
	copy(pkt[ip4HeaderLen:], payload)
	return pkt
}

// buildIPv6 assembles an IPv6 packet from a complete payload slice.
func buildIPv6(tc byte, next uint8, hop byte, src, dst [16]byte, payload []byte) []byte {
	pkt := make([]byte, ip6HeaderLen+len(payload))
	pkt[ip6VersionTC] = 0x60 | (tc >> 4)
	pkt[ip6Flow] = tc << 4
	binary.BigEndian.PutUint16(pkt[ip6Payload:], uint16(len(payload)))
	pkt[ip6NextHdr] = next
	pkt[ip6HopLimit] = decrementHop(hop)
	copy(pkt[ip6Src:ip6Src+16], src[:])
	copy(pkt[ip6Dst:ip6Dst+16], dst[:])
	copy(pkt[ip6HeaderLen:], payload)
	return pkt
}

// decrementHop implements RFC 7915 section 4.3: the limit is reduced by one
// and floored at 1 so a translated packet is never dropped here.
func decrementHop(h byte) byte {
	if h <= 1 {
		return 1
	}
	return h - 1
}

// translateV6ToV4 rewrites a complete IPv6 packet into an IPv4 packet using
// the already-resolved BIB port pair (newSPort/newDPort).
func (e *Engine) translateV6ToV4(p6 ipv6Packet, newSPort, newDPort uint16, v4src, v4dst [4]byte) []byte {
	l4 := append([]byte(nil), p6.payload...)
	if _, _, ok := l4Ports(p6.next, l4); ok {
		setPorts(l4, newSPort, newDPort)
		recomputeL4(p6.next, l4, v4src, v4dst, [16]byte{}, [16]byte{}, dirV4)
	}
	return buildIPv4(p6.tc, e.nextIPID(), p6.next, p6.hop, v4src, v4dst, l4)
}

// translateV4ToV6 rewrites a complete IPv4 packet into an IPv6 packet.
func (e *Engine) translateV4ToV6(p4 ipv4Packet, newSPort, newDPort uint16, v6src, v6dst [16]byte) []byte {
	l4 := append([]byte(nil), p4.payload...)
	if _, _, ok := l4Ports(p4.proto, l4); ok {
		setPorts(l4, newSPort, newDPort)
		recomputeL4(p4.proto, l4, [4]byte{}, [4]byte{}, v6src, v6dst, dirV6)
	}
	return buildIPv6(p4.tos, p4.proto, p4.ttl, v6src, v6dst, l4)
}
