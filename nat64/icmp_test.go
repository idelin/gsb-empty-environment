package nat64

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

func seedUDPSession(t *testing.T, e *Engine, sport, dport uint16, remote netip.Addr) *BIBEntry {
	t.Helper()
	src := mustV6(client6.String())
	dst := mustV6(e.synthV6(remote).String())
	e.HandleV6(buildUDP6(src, dst, sport, dport, []byte("hello")))
	bib := e.lookupBIBV6(ProtoUDP, client6, sport)
	if bib == nil {
		t.Fatal("seed BIB missing")
	}
	return bib
}

// ICMPv6 Packet Too Big is translated to ICMPv4 Frag Needed: outer
// type/code, MTU minus 20, and the embedded triggering packet's five
// tuple are all rewritten; incremental checksums stay verifiable.
func TestICMPv6PacketTooBigNestedTranslation(t *testing.T) {
	onV4, v4out := collect()
	e, _ := newTestEngine(t)
	e.cfg.OnV4 = onV4

	const (
		sport uint16 = 51000
		dport uint16 = 500
		mtu6  uint32 = 1400
	)
	bib := seedUDPSession(t, e, sport, dport, server4)

	// Build the triggering IPv6 packet, then truncate it as a real PTB
	// quote would be.
	trigger := buildUDP6(mustV6(client6.String()), mustV6(e.synthV6(server4).String()),
		sport, dport, []byte("0123456789abcdef"))
	quoted := trigger[:ip6HeaderLen+udpMinLen+4]

	icmp6 := make([]byte, icmpHeader+len(quoted))
	icmp6[icmpType] = icmp6PacketTooBig
	icmp6[icmpCode] = 0
	binary.BigEndian.PutUint32(icmp6[icmpRest:], mtu6)
	copy(icmp6[icmpHeader:], quoted)
	binary.BigEndian.PutUint16(icmp6[icmpChecksum:],
		transportChecksumV6(mustV6(e.synthV6(server4).String()), mustV6(client6.String()), icmpv6Proto, icmp6))

	outer6 := buildIPv6(0, icmpv6Proto, 64,
		mustV6(e.synthV6(server4).String()), mustV6(client6.String()), icmp6)
	e.HandleV6(outer6)

	// Index 0 is the seeded UDP flow; the translated PTB must be index 1.
	if len(*v4out) != 2 {
		t.Fatalf("expected seed flow + one ICMPv4 packet, got %d", len(*v4out))
	}
	out := (*v4out)[1]
	p4, ok := parseIPv4(out)
	if !ok || p4.proto != 1 {
		t.Fatal("output is not ICMPv4")
	}
	if p4.payload[icmpType] != icmp4DestUnreach || p4.payload[icmpCode] != 4 {
		t.Fatalf("outer mapping wrong: type=%d code=%d", p4.payload[icmpType], p4.payload[icmpCode])
	}
	if got := binary.BigEndian.Uint16(p4.payload[icmpRest+2:]); got != uint16(mtu6-20) {
		t.Fatalf("MTU = %d, want %d", got, mtu6-20)
	}
	icmpCopy := append([]byte(nil), p4.payload...)
	savedICMP := binary.BigEndian.Uint16(icmpCopy[icmpChecksum:])
	icmpCopy[icmpChecksum], icmpCopy[icmpChecksum+1] = 0, 0
	if got := checksum(icmpCopy); got != savedICMP {
		t.Fatalf("outer ICMPv4 checksum mismatch: wire=%#x recompute=%#x", savedICMP, got)
	}

	// Outer header mirrors the triggering flow's translated five tuple.
	if p4.src != server4.As4() || p4.dst != bib.V4Addr.As4() {
		t.Fatalf("outer tuple wrong: %v -> %v", p4.src, p4.dst)
	}

	inner4, ok := parseIPv4(p4.payload[icmpHeader:])
	if !ok {
		t.Fatal("embedded packet is not IPv4")
	}
	if inner4.src != bib.V4Addr.As4() || inner4.dst != server4.As4() {
		t.Fatalf("inner addresses wrong: %v -> %v", inner4.src, inner4.dst)
	}
	isport := binary.BigEndian.Uint16(inner4.payload[udpSrcPort:])
	idport := binary.BigEndian.Uint16(inner4.payload[udpDstPort:])
	if isport != bib.V4Port || idport != dport {
		t.Fatalf("inner ports wrong: %d -> %d", isport, idport)
	}

	// Incremental (RFC 1624) checksum must match a full recomputation over
	// the fully translated, untruncated packet.
	full := e.translateV6ToV4(mustParseV6(t, trigger), bib.V4Port, dport,
		bib.V4Addr.As4(), server4.As4())
	fullP4, _ := parseIPv4(full)
	wantCS := binary.BigEndian.Uint16(fullP4.payload[udpCheck:])
	gotCS := binary.BigEndian.Uint16(inner4.payload[udpCheck:])
	if gotCS != wantCS {
		t.Fatalf("incremental checksum %#x != full recompute %#x", gotCS, wantCS)
	}

	// The checksum field is present and non-zero on the wire.
	if gotCS == 0 {
		t.Fatal("embedded UDP checksum is zero")
	}
}

// TCP variant plus the reverse ICMPv4 Frag Needed -> ICMPv6 PTB path with
// the 1280 floor and MTU +20.
func TestICMPv4FragNeededReverseAndMTUClamp(t *testing.T) {
	onV6, v6out := collect()
	e, _ := newTestEngine(t)
	e.cfg.OnV6 = onV6

	const (
		sport uint16 = 52000
		dport uint16 = 8080
	)
	bib := seedOutboundTCP(t, e, sport, dport, server4)

	trigger4 := buildTCP4(bib.V4Addr.As4(), server4.As4(), bib.V4Port, dport, tcpACK, nil)
	quoted := trigger4[:ip4HeaderLen+tcpMinLen]

	icmp4 := make([]byte, icmpHeader+len(quoted))
	icmp4[icmpType] = icmp4DestUnreach
	icmp4[icmpCode] = 4
	binary.BigEndian.PutUint16(icmp4[icmpRest+2:], 1200) // below v6 floor
	copy(icmp4[icmpHeader:], quoted)
	binary.BigEndian.PutUint16(icmp4[icmpChecksum:], checksum(icmp4))

	e.HandleV4(buildIPv4(0, 0, 1, 64, server4.As4(), bib.V4Addr.As4(), icmp4))

	if len(*v6out) != 1 {
		t.Fatalf("expected one ICMPv6 packet, got %d", len(*v6out))
	}
	p6, ok := parseIPv6((*v6out)[0])
	if !ok || p6.next != icmpv6Proto {
		t.Fatal("output is not ICMPv6")
	}
	if p6.payload[icmpType] != icmp6PacketTooBig {
		t.Fatalf("outer type = %d, want PTB(2)", p6.payload[icmpType])
	}
	if got := binary.BigEndian.Uint32(p6.payload[icmpRest:]); got != 1280 {
		t.Fatalf("clamped MTU = %d, want 1280", got)
	}
	icmpCopy6 := append([]byte(nil), p6.payload...)
	savedV6ICMP := binary.BigEndian.Uint16(icmpCopy6[icmpChecksum:])
	icmpCopy6[icmpChecksum], icmpCopy6[icmpChecksum+1] = 0, 0
	if got := transportChecksumV6(p6.src, p6.dst, icmpv6Proto, icmpCopy6); got != savedV6ICMP {
		t.Fatalf("outer ICMPv6 checksum mismatch: wire=%#x recompute=%#x", savedV6ICMP, got)
	}

	inner6, ok := parseIPv6(p6.payload[icmpHeader:])
	if !ok {
		t.Fatal("embedded packet is not IPv6")
	}
	if inner6.src != bib.V6Addr.As16() {
		t.Fatal("inner v6 source wrong")
	}
	if binary.BigEndian.Uint16(inner6.payload[tcpSrcPort:]) != bib.V6Port {
		t.Fatal("inner TCP source port not reverse-mapped")
	}

	// MTU above the floor adds 20 without clamping.
	icmp4[icmpChecksum], icmp4[icmpChecksum+1] = 0, 0
	binary.BigEndian.PutUint16(icmp4[icmpRest+2:], 1480)
	binary.BigEndian.PutUint16(icmp4[icmpChecksum:], checksum(icmp4))
	*v6out = nil
	e.HandleV4(buildIPv4(0, 0, 1, 64, server4.As4(), bib.V4Addr.As4(), icmp4))
	p6, _ = parseIPv6((*v6out)[0])
	if got := binary.BigEndian.Uint32(p6.payload[icmpRest:]); got != 1500 {
		t.Fatalf("MTU = %d, want 1500", got)
	}
}

// ICMPv6 Time Exceeded maps type 3 -> ICMPv4 type 11 with code preserved.
func TestICMPv6TimeExceededMapping(t *testing.T) {
	onV4, v4out := collect()
	e, _ := newTestEngine(t)
	e.cfg.OnV4 = onV4
	bib := seedUDPSession(t, e, 51001, 53, other4)

	trigger := buildUDP6(mustV6(client6.String()), mustV6(e.synthV6(other4).String()),
		51001, 53, []byte("dns"))
	trigger = trigger[:ip6HeaderLen+udpMinLen]
	icmp6 := make([]byte, icmpHeader+len(trigger))
	icmp6[icmpType] = icmp6TimeExceeded
	icmp6[icmpCode] = 0
	copy(icmp6[icmpHeader:], trigger)
	binary.BigEndian.PutUint16(icmp6[icmpChecksum:],
		transportChecksumV6(mustV6(e.synthV6(other4).String()), mustV6(client6.String()), icmpv6Proto, icmp6))

	e.HandleV6(buildIPv6(0, icmpv6Proto, 64,
		mustV6(e.synthV6(other4).String()), mustV6(client6.String()), icmp6))

	p4, ok := parseIPv4((*v4out)[len(*v4out)-1])
	if !ok || p4.payload[icmpType] != icmp4TimeExceeded || p4.payload[icmpCode] != 0 {
		t.Fatalf("time exceeded mapping wrong: %+v", p4.payload[:4])
	}
	inner, _ := parseIPv4(p4.payload[icmpHeader:])
	if binary.BigEndian.Uint16(inner.payload[udpSrcPort:]) != bib.V4Port {
		t.Fatal("inner UDP source port wrong")
	}
}

// Errors without a matching session cannot be reverse-mapped and are dropped.
func TestICMPUnknownFlowDropped(t *testing.T) {
	onV4, v4out := collect()
	e, _ := newTestEngine(t)
	e.cfg.OnV4 = onV4

	dead := netip.MustParseAddr("2001:db8::dead")
	trigger := buildUDP6(mustV6(dead.String()), mustV6(e.synthV6(server4).String()),
		9999, 9999, []byte("x"))[:ip6HeaderLen+udpMinLen]
	icmp6 := make([]byte, icmpHeader+len(trigger))
	icmp6[icmpType] = icmp6PacketTooBig
	binary.BigEndian.PutUint32(icmp6[icmpRest:], 1280)
	copy(icmp6[icmpHeader:], trigger)
	binary.BigEndian.PutUint16(icmp6[icmpChecksum:],
		transportChecksumV6(mustV6(e.synthV6(server4).String()), mustV6(dead.String()), icmpv6Proto, icmp6))

	e.HandleV6(buildIPv6(0, icmpv6Proto, 64,
		mustV6(e.synthV6(server4).String()), mustV6(dead.String()), icmp6))

	if len(*v4out) != 0 || e.stats.Drops == 0 {
		t.Fatal("unmappable ICMP error must be dropped")
	}
}
