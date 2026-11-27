package nat64

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"
)

var (
	client6 = netip.MustParseAddr("2001:db8::1")
	server4 = netip.MustParseAddr("198.51.100.7")
	other4  = netip.MustParseAddr("203.0.113.9")
	poolA   = netip.MustParseAddr("192.0.2.10")
	poolB   = netip.MustParseAddr("192.0.2.11")
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time      { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }

func newTestEngine(t *testing.T, pool ...netip.Addr) (*Engine, *fakeClock) {
	t.Helper()
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	if len(pool) == 0 {
		pool = []netip.Addr{poolA}
	}
	e, err := New(Config{PoolV4: pool, Now: clk.now})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e, clk
}

func mustV6(addr string) [16]byte { return netip.MustParseAddr(addr).As16() }
func mustV4(addr string) [4]byte  { return netip.MustParseAddr(addr).As4() }

// buildTCP6 builds a complete IPv6 TCP packet with a correct checksum.
func buildTCP6(src, dst [16]byte, sport, dport uint16, flags byte, payload []byte) []byte {
	tcpHdr := make([]byte, tcpMinLen+len(payload))
	binary.BigEndian.PutUint16(tcpHdr[tcpSrcPort:], sport)
	binary.BigEndian.PutUint16(tcpHdr[tcpDstPort:], dport)
	binary.BigEndian.PutUint32(tcpHdr[tcpSeq:], 1000)
	binary.BigEndian.PutUint32(tcpHdr[tcpAck:], 2000)
	tcpHdr[tcpDataOff] = 5 << 4
	tcpHdr[tcpFlags] = flags
	binary.BigEndian.PutUint16(tcpHdr[tcpWindow:], 65535)
	copy(tcpHdr[tcpMinLen:], payload)
	cs := transportChecksumV6(src, dst, ProtoTCP, tcpHdr)
	binary.BigEndian.PutUint16(tcpHdr[tcpCheck:], cs)
	return buildIPv6(0, ProtoTCP, 64, src, dst, tcpHdr)
}

func buildUDP6(src, dst [16]byte, sport, dport uint16, payload []byte) []byte {
	udpHdr := make([]byte, udpMinLen+len(payload))
	binary.BigEndian.PutUint16(udpHdr[udpSrcPort:], sport)
	binary.BigEndian.PutUint16(udpHdr[udpDstPort:], dport)
	binary.BigEndian.PutUint16(udpHdr[udpLength:], uint16(len(udpHdr)))
	copy(udpHdr[udpMinLen:], payload)
	cs := transportChecksumV6(src, dst, ProtoUDP, udpHdr)
	binary.BigEndian.PutUint16(udpHdr[udpCheck:], cs)
	return buildIPv6(0, ProtoUDP, 64, src, dst, udpHdr)
}

func buildTCP4(src, dst [4]byte, sport, dport uint16, flags byte, payload []byte) []byte {
	tcpHdr := make([]byte, tcpMinLen+len(payload))
	binary.BigEndian.PutUint16(tcpHdr[tcpSrcPort:], sport)
	binary.BigEndian.PutUint16(tcpHdr[tcpDstPort:], dport)
	binary.BigEndian.PutUint32(tcpHdr[tcpSeq:], 3000)
	binary.BigEndian.PutUint32(tcpHdr[tcpAck:], 4000)
	tcpHdr[tcpDataOff] = 5 << 4
	tcpHdr[tcpFlags] = flags
	binary.BigEndian.PutUint16(tcpHdr[tcpWindow:], 65535)
	copy(tcpHdr[tcpMinLen:], payload)
	cs := transportChecksumV4(src, dst, ProtoTCP, tcpHdr)
	binary.BigEndian.PutUint16(tcpHdr[tcpCheck:], cs)
	return buildIPv4(0, 0, ProtoTCP, 64, src, dst, tcpHdr)
}

func buildUDP4(src, dst [4]byte, sport, dport uint16, payload []byte) []byte {
	udpHdr := make([]byte, udpMinLen+len(payload))
	binary.BigEndian.PutUint16(udpHdr[udpSrcPort:], sport)
	binary.BigEndian.PutUint16(udpHdr[udpDstPort:], dport)
	binary.BigEndian.PutUint16(udpHdr[udpLength:], uint16(len(udpHdr)))
	copy(udpHdr[udpMinLen:], payload)
	cs := transportChecksumV4(src, dst, ProtoUDP, udpHdr)
	if cs == 0 {
		cs = 0xffff
	}
	binary.BigEndian.PutUint16(udpHdr[udpCheck:], cs)
	return buildIPv4(0, 0, ProtoUDP, 64, src, dst, udpHdr)
}

func verifyV4Checksum(t *testing.T, pkt []byte, proto uint8) {
	t.Helper()
	original := append([]byte(nil), pkt...)
	p, ok := parseIPv4(pkt)
	if !ok {
		t.Fatalf("not a valid IPv4 packet")
	}
	hdr := append([]byte(nil), original[:ip4HeaderLen]...)
	savedHdr := binary.BigEndian.Uint16(hdr[ip4Checksum:])
	hdr[ip4Checksum], hdr[ip4Checksum+1] = 0, 0
	if got := checksum(hdr); got != savedHdr {
		t.Fatalf("IPv4 header checksum mismatch: wire=%#x recompute=%#x", savedHdr, got)
	}
	p, _ = parseIPv4(original)
	switch proto {
	case ProtoTCP:
		l4 := append([]byte(nil), p.payload...)
		saved := binary.BigEndian.Uint16(l4[tcpCheck:])
		l4[tcpCheck], l4[tcpCheck+1] = 0, 0
		if want := transportChecksumV4(p.src, p.dst, ProtoTCP, l4); want != saved {
			t.Fatalf("TCP checksum mismatch: wire=%#x recompute=%#x", saved, want)
		}
	case ProtoUDP:
		l4 := append([]byte(nil), p.payload...)
		saved := binary.BigEndian.Uint16(l4[udpCheck:])
		l4[udpCheck], l4[udpCheck+1] = 0, 0
		want := transportChecksumV4(p.src, p.dst, ProtoUDP, l4)
		if want == 0 {
			want = 0xffff
		}
		if want != saved {
			t.Fatalf("UDP checksum mismatch: wire=%#x recompute=%#x", saved, want)
		}
	}
}

func verifyV6Checksum(t *testing.T, pkt []byte, proto uint8) {
	t.Helper()
	p, ok := parseIPv6(pkt)
	if !ok {
		t.Fatalf("not a valid IPv6 packet")
	}
	switch proto {
	case ProtoTCP:
		l4 := append([]byte(nil), p.payload...)
		saved := binary.BigEndian.Uint16(l4[tcpCheck:])
		l4[tcpCheck], l4[tcpCheck+1] = 0, 0
		if got := transportChecksumV6(p.src, p.dst, ProtoTCP, l4); got != saved {
			t.Fatalf("v6 TCP checksum mismatch: wire=%#x recompute=%#x", saved, got)
		}
	case ProtoUDP:
		l4 := append([]byte(nil), p.payload...)
		saved := binary.BigEndian.Uint16(l4[udpCheck:])
		l4[udpCheck], l4[udpCheck+1] = 0, 0
		if got := transportChecksumV6(p.src, p.dst, ProtoUDP, l4); got != saved {
			t.Fatalf("v6 UDP checksum mismatch: wire=%#x recompute=%#x", saved, got)
		}
	}
}

func collect() (func([]byte), *[][]byte) {
	var out [][]byte
	cb := func(p []byte) { out = append(out, append([]byte(nil), p...)) }
	return cb, &out
}

func mustParseV6(t *testing.T, pkt []byte) ipv6Packet {
	t.Helper()
	p, ok := parseIPv6(pkt)
	if !ok {
		t.Fatal("mustParseV6: invalid packet")
	}
	return p
}
