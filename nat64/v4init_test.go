package nat64

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"
)

func seedOutboundTCP(t *testing.T, e *Engine, sport, dport uint16, remote netip.Addr) *BIBEntry {
	t.Helper()
	src := mustV6(client6.String())
	dst := mustV6(e.synthV6(remote).String())
	e.HandleV6(buildTCP6(src, dst, sport, dport, tcpSYN, nil))
	bib := e.lookupBIBV6(ProtoTCP, client6, sport)
	if bib == nil {
		t.Fatal("seed BIB missing")
	}
	s := e.sessV6[V6SessionKey{Src: client6, Dst: e.synthV6(remote), Proto: ProtoTCP, SPort: sport, DPort: dport}]
	if s == nil || s.State != StateV6Init {
		t.Fatalf("seed session wrong: %+v", s)
	}
	return bib
}

// An unsolicited IPv4 SYN targeting an existing BIB mapping enters
// V4_INIT, emits nothing, caches the SYN and arms the 6-second timer.
func TestV4InitHoldsUnsolicitedSYN(t *testing.T) {
	onV4, v4out := collect()
	onV6, v6out := collect()
	e, clk := newTestEngine(t)
	e.cfg.OnV4, e.cfg.OnV6 = onV4, onV6

	bib := seedOutboundTCP(t, e, 45000, 22, server4)

	syn := buildTCP4(server4.As4(), bib.V4Addr.As4(), 54000, bib.V4Port, tcpSYN, nil)
	e.HandleV4(syn)

	if len(*v4out) != 1 || len(*v6out) != 0 {
		t.Fatalf("V4_INIT must not forward immediately; v4=%d v6=%d", len(*v4out), len(*v6out))
	}
	if e.SessionCount() != 2 { // original V6_INIT plus the V4_INIT hold
		t.Fatalf("expected 2 sessions, got %d", e.SessionCount())
	}
	holdKey := V4SessionKey{Src: server4, Dst: bib.V4Addr, Proto: ProtoTCP, SPort: 54000, DPort: bib.V4Port}
	hold := e.sessV4[holdKey]
	if hold == nil || hold.State != StateV4Init {
		t.Fatalf("V4_INIT session missing: %+v", hold)
	}
	if !hold.ExpiresAt.Equal(clk.t.Add(6 * time.Second)) {
		t.Fatalf("V4_INIT timer = %v, want +6s", hold.ExpiresAt.Sub(clk.t))
	}
	if len(hold.CachedV4SYN) == 0 {
		t.Fatal("cached SYN not stored")
	}

	// Before the timer fires, sweeping changes nothing.
	clk.add(5 * time.Second)
	e.Sweep()
	if e.sessV4[holdKey] == nil {
		t.Fatal("V4_INIT expired before 6 seconds")
	}
}

// Within 6 seconds the IPv6 host's simultaneous SYN pairs the hold, which
// converges to ESTABLISHED, flushes the cached SYN and forwards both ways.
func TestV4InitPairWithinSixSeconds(t *testing.T) {
	onV4, v4out := collect()
	onV6, v6out := collect()
	e, clk := newTestEngine(t)
	e.cfg.OnV4, e.cfg.OnV6 = onV4, onV6

	const clientPort uint16 = 54002
	const remotePort uint16 = 7000
	// A BIB mapping already exists (created by an earlier flow that has
	// since closed), but no session for this remote endpoint is live.
	bib, ok := e.allocateBIB(ProtoTCP, client6, clientPort)
	if !ok {
		t.Fatal("seed BIB allocation failed")
	}

	// Unsolicited SYN from the remote: (server4, remotePort) -> pool mapping.
	e.HandleV4(buildTCP4(server4.As4(), bib.V4Addr.As4(), remotePort, bib.V4Port, tcpSYN, nil))
	clk.add(4 * time.Second)

	// IPv6 simultaneous open: same client source port, same remote port.
	src := mustV6(client6.String())
	dst := mustV6(e.synthV6(server4).String())
	e.HandleV6(buildTCP6(src, dst, clientPort, remotePort, tcpSYN, nil))

	if len(*v6out) != 1 {
		t.Fatalf("cached SYN must be flushed once, got %d", len(*v6out))
	}
	// The pairing emits the fresh outbound SYN toward IPv4 once.
	if len(*v4out) != 1 {
		t.Fatalf("expected the paired outbound SYN, got v4=%d", len(*v4out))
	}

	paired := e.sessV6[V6SessionKey{
		Src: client6, Dst: e.synthV6(server4), Proto: ProtoTCP,
		SPort: clientPort, DPort: remotePort,
	}]
	if paired == nil || paired.State != StateV6Established {
		t.Fatalf("paired session state = %v, want V6_ESTABLISHED", paired)
	}
	if len(paired.CachedV4SYN) != 0 {
		t.Fatal("cached SYN must be cleared after pairing")
	}

	// The previously cached SYN reached IPv6 translated correctly.
	cached := (*v6out)[0]
	p6, ok := parseIPv6(cached)
	if !ok {
		t.Fatal("cached output is not IPv6")
	}
	if addr16(p6.dst) != client6 {
		t.Fatal("cached SYN delivered to wrong IPv6 client")
	}
	if binary.BigEndian.Uint16(p6.payload[tcpSrcPort:]) != remotePort {
		t.Fatal("cached SYN source port wrong")
	}
	verifyV6Checksum(t, cached, ProtoTCP)

	// After pairing the timer must be the 2h established timer, not 6s.
	clk.add(7 * time.Second)
	e.Sweep()
	if e.SessionCount() != 1 {
		t.Fatalf("paired session survived past 6s; count=%d", e.SessionCount())
	}
}

// After the 6-second hold expires unpaired, the temporary session and its
// BIB are removed and ICMPv4 Destination Unreachable Code 13 is returned.
func TestV4InitTimeoutSendsICMP13(t *testing.T) {
	onV4, v4out := collect()
	onV6, v6out := collect()
	e, clk := newTestEngine(t)
	e.cfg.OnV4, e.cfg.OnV6 = onV4, onV6

	const clientPort uint16 = 54003
	const remotePort uint16 = 8000
	bib, ok := e.allocateBIB(ProtoTCP, client6, clientPort)
	if !ok {
		t.Fatal("seed BIB allocation failed")
	}

	e.HandleV4(buildTCP4(server4.As4(), bib.V4Addr.As4(), remotePort, bib.V4Port, tcpSYN, nil))
	if e.BIBCount() != 1 {
		t.Fatalf("BIB should still exist during hold, got %d", e.BIBCount())
	}

	clk.add(6 * time.Second)
	e.Sweep()

	if e.SessionCount() != 0 {
		t.Fatalf("V4_INIT session must be deleted, count=%d", e.SessionCount())
	}
	if e.BIBCount() != 0 {
		t.Fatalf("BIB must be cascade-deleted at ref zero, count=%d", e.BIBCount())
	}
	if e.stats.V4InitTimeouts != 1 {
		t.Fatalf("V4InitTimeouts=%d, want 1", e.stats.V4InitTimeouts)
	}

	var icmpPkt []byte
	for _, pkt := range *v4out {
		if p, ok := parseIPv4(pkt); ok && p.proto == 1 {
			icmpPkt = pkt
		}
	}
	if icmpPkt == nil {
		t.Fatal("no ICMPv4 error emitted")
	}
	p4, _ := parseIPv4(icmpPkt)
	if p4.src != bib.V4Addr.As4() || p4.dst != server4.As4() {
		t.Fatal("ICMP outer addresses wrong")
	}
	if p4.payload[icmpType] != icmp4DestUnreach || p4.payload[icmpCode] != 13 {
		t.Fatalf("want ICMPv4 unreachable code 13, got type=%d code=%d",
			p4.payload[icmpType], p4.payload[icmpCode])
	}
	icmpCopy := append([]byte(nil), p4.payload...)
	savedICMP := binary.BigEndian.Uint16(icmpCopy[icmpChecksum:])
	icmpCopy[icmpChecksum], icmpCopy[icmpChecksum+1] = 0, 0
	// The quoted packet's IP header checksum is cleared per RFC 792.
	if len(icmpCopy) >= icmpHeader+ip4HeaderLen {
		off := icmpHeader + ip4Checksum // ip4Checksum already equals 10
		icmpCopy[off], icmpCopy[off+1] = 0, 0
	}
	if got := checksum(icmpCopy); got != savedICMP {
		t.Fatalf("ICMPv4 checksum mismatch: wire=%#x recompute=%#x", savedICMP, got)
	}
	if len(*v6out) != 0 {
		t.Fatal("nothing may be forwarded to IPv6 on timeout")
	}
}

// A SYN aimed at a pool port with no BIB is an external scan and is
// silently dropped without creating any state.
// A V6 SYN toward a different remote while a V4_INIT hold is live must not
// disturb the hold; it creates an independent V6_INIT session instead.
func TestV4InitNotBrokenByUnrelatedSYN(t *testing.T) {
	onV4, _ := collect()
	onV6, v6out := collect()
	e, _ := newTestEngine(t)
	e.cfg.OnV4, e.cfg.OnV6 = onV4, onV6

	const clientPort uint16 = 55000
	bib, ok := e.allocateBIB(ProtoTCP, client6, clientPort)
	if !ok {
		t.Fatal("BIB allocation failed")
	}
	// Hold awaiting server4.
	e.HandleV4(buildTCP4(server4.As4(), bib.V4Addr.As4(), 7000, bib.V4Port, tcpSYN, nil))

	// Client opens toward a different IPv4 destination using the same BIB.
	src := mustV6(client6.String())
	dst := mustV6(e.synthV6(other4).String())
	e.HandleV6(buildTCP6(src, dst, clientPort, 9000, tcpSYN, nil))

	if len(*v6out) != 0 {
		t.Fatal("the hold's cached SYN must not be flushed by an unrelated SYN")
	}
	if e.SessionCount() != 2 {
		t.Fatalf("expected hold + independent session, got %d", e.SessionCount())
	}
	holdKey := V4SessionKey{Src: server4, Dst: bib.V4Addr, Proto: ProtoTCP, SPort: 7000, DPort: bib.V4Port}
	if e.sessV4[holdKey] == nil || e.sessV4[holdKey].State != StateV4Init {
		t.Fatal("V4_INIT hold was disturbed by an unrelated SYN")
	}
}

func TestV4SYNNoBIBIsDropped(t *testing.T) {
	onV4, v4out := collect()
	onV6, v6out := collect()
	e, _ := newTestEngine(t)
	e.cfg.OnV4, e.cfg.OnV6 = onV4, onV6

	e.HandleV4(buildTCP4(server4.As4(), poolA.As4(), 33333, 1234, tcpSYN, nil))
	if e.SessionCount() != 0 || e.BIBCount() != 0 {
		t.Fatal("scan created state")
	}
	if len(*v4out) != 0 || len(*v6out) != 0 || e.stats.Drops == 0 {
		t.Fatal("scan must be silently dropped")
	}
}
