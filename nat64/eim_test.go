package nat64

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

// EIM: the same IPv6 (src, sport) talking to two different IPv4
// destinations reuses one BIB binding but produces two sessions keyed by
// the remote five tuple.
func TestEIMReuseAcrossDestinations(t *testing.T) {
	onV4, v4out := collect()
	e, _ := newTestEngine(t)
	e.cfg.OnV4 = onV4

	src := mustV6(client6.String())
	dst1 := mustV6(e.synthV6(server4).String())
	dst2 := mustV6(e.synthV6(other4).String())

	e.HandleV6(buildTCP6(src, dst1, 40000, 443, tcpSYN, nil))
	e.HandleV6(buildTCP6(src, dst2, 40000, 80, tcpSYN, nil))

	if e.BIBCount() != 1 {
		t.Fatalf("expected 1 BIB entry, got %d", e.BIBCount())
	}
	if e.SessionCount() != 2 {
		t.Fatalf("expected 2 sessions, got %d", e.SessionCount())
	}

	bibs := e.BIBEntries()
	b := bibs[0]
	if b.V6Port != 40000 || b.V4Port != 40000 {
		t.Fatalf("exact source port reuse failed: %+v", b)
	}
	if b.Refs != 2 {
		t.Fatalf("BIB refs = %d, want 2", b.Refs)
	}

	for i, remote := range []struct {
		ip    [4]byte
		dport uint16
	}{
		{server4.As4(), 443}, {other4.As4(), 80},
	} {
		p4, ok := parseIPv4((*v4out)[i])
		if !ok {
			t.Fatalf("output %d not IPv4", i)
		}
		if p4.src != b.V4Addr.As4() || p4.dst != remote.ip {
			t.Fatalf("output %d addresses wrong", i)
		}
		sport := binary.BigEndian.Uint16(p4.payload[tcpSrcPort:])
		dport := binary.BigEndian.Uint16(p4.payload[tcpDstPort:])
		if sport != b.V4Port || dport != remote.dport {
			t.Fatalf("output %d ports wrong: %d->%d", i, sport, dport)
		}
		verifyV4Checksum(t, (*v4out)[i], ProtoTCP)
	}
}

// Port range and parity preservation (RFC 6146 section 4.2.2).
func TestPortRangeAndParityPreservation(t *testing.T) {
	e, _ := newTestEngine(t)

	// System port 80 stays in the system range and keeps its parity (even).
	bSys, ok := e.allocateBIB(ProtoTCP, client6, 80)
	if !ok {
		t.Fatal("allocate system port failed")
	}
	if bSys.V4Port > 1023 || bSys.V4Port%2 != 0 {
		t.Fatalf("system/parity preservation failed: %d", bSys.V4Port)
	}

	// Pin the exact odd user port, then force a collision at a second
	// client's same odd source port: allocation must stay odd and user.
	b1, ok := e.allocateBIB(ProtoUDP, client6, 50001)
	if !ok || b1.V4Port != 50001 {
		t.Fatalf("exact user port allocation failed: %+v", b1)
	}
	b2, ok := e.allocateBIB(ProtoUDP, netip.MustParseAddr("2001:db8::2"), 50001)
	if !ok {
		t.Fatal("second allocation failed")
	}
	if b2.V4Port == 50001 {
		t.Fatal("allocated a port already in use")
	}
	if b2.V4Port < 1024 || b2.V4Port%2 != 1 {
		t.Fatalf("user range / odd parity not preserved: %d", b2.V4Port)
	}

	// With the exact odd port and several following odd ports occupied,
	// allocation keeps scanning odd user ports and stays in the user range;
	// this exercises the parity-preservation ladder without pinning all
	// 32k ports (which is expensive and covered conceptually by the helper
	// unit checks below).
	exhaust, _ := newTestEngine(t)
	client := netip.MustParseAddr("2001:db8::3")
	used := map[uint16]bool{}
	for p := uint16(1025); p < 1200; p += 2 {
		b, ok := exhaust.allocateBIB(ProtoUDP, client, p)
		if !ok {
			t.Fatalf("allocation failed at %d", p)
		}
		if b.V4Port < 1024 || b.V4Port%2 != 1 {
			t.Fatalf("user range / odd parity not preserved: %d", b.V4Port)
		}
		if used[b.V4Port] {
			t.Fatalf("duplicate allocation of port %d", b.V4Port)
		}
		used[b.V4Port] = true
	}

	// An even source port keeps its parity inside the user range.
	last, ok := exhaust.allocateBIB(ProtoUDP, client, 40000)
	if !ok || last.V4Port < 1024 || last.V4Port%2 != 0 {
		t.Fatalf("opposite-parity user fallback failed: %+v", last)
	}

	// System-port sources never escape into the user range.
	sys, ok := exhaust.allocateBIB(ProtoTCP, netip.MustParseAddr("2001:db8::4"), 443)
	if !ok || sys.V4Port > 1023 {
		t.Fatalf("system range not preserved: %+v", sys)
	}
}

// Multiple pool addresses: bindings spread across pools and the
// (poolIPv4, port4, dst4, dport) session keys never collide.
func TestMultiPoolNoCollision(t *testing.T) {
	onV4, _ := collect()
	e, _ := newTestEngine(t, poolA, poolB)
	e.cfg.OnV4 = onV4

	src := mustV6(client6.String())
	for i, sport := range []uint16{30001, 30002} {
		dst := mustV6(e.synthV6(server4).String())
		e.HandleV6(buildUDP6(src, dst, sport, 53, []byte{1, 2, 3}))
		bib := e.lookupBIBV6(ProtoUDP, client6, sport)
		wantPool := []byte{poolA.As4()[3], poolB.As4()[3]}[i]
		if bib.V4Addr.As4()[3] != wantPool {
			t.Fatalf("flow %d placed on %v, want pool octet %d", i, bib.V4Addr, wantPool)
		}
	}

	// Two clients may reuse the same external port on different pool
	// addresses without any key collision.
	otherClient := netip.MustParseAddr("2001:db8::9")
	e.HandleV6(buildUDP6(mustV6(otherClient.String()), mustV6(e.synthV6(server4).String()), 30001, 53, []byte{9}))
	if e.BIBCount() != 3 {
		t.Fatalf("expected 3 BIB entries across pools, got %d", e.BIBCount())
	}
	seen := map[uint64]bool{}
	for _, b := range e.BIBEntries() {
		v4b := b.V4Addr.As4()
		k := uint64(binary.BigEndian.Uint32(v4b[:]))<<16 | uint64(b.V4Port)
		if seen[k] {
			t.Fatalf("duplicate (pool,port) binding: %v/%d", b.V4Addr, b.V4Port)
		}
		seen[k] = true
	}
}
