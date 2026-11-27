package nat64

import (
	"encoding/binary"
	"testing"
	"time"
)

// UDP sessions time out after 5 minutes of inactivity and remove the BIB
// when they were its last reference; traffic refreshes the timer.
func TestUDPTimeoutAndBIBCascade(t *testing.T) {
	e, clk := newTestEngine(t)
	src := mustV6(client6.String())
	dst := mustV6(e.synthV6(server4).String())

	e.HandleV6(buildUDP6(src, dst, 40000, 53, []byte("q")))
	if e.SessionCount() != 1 || e.BIBCount() != 1 {
		t.Fatalf("unexpected counts: sessions=%d bib=%d", e.SessionCount(), e.BIBCount())
	}

	clk.add(4*time.Minute + 59*time.Second)
	e.Sweep()
	if e.SessionCount() != 1 {
		t.Fatal("UDP session expired before 5 minutes")
	}

	// A reply refreshes the inactivity timer.
	bib := e.lookupBIBV6(ProtoUDP, client6, 40000)
	e.HandleV4(buildUDP4(server4.As4(), bib.V4Addr.As4(), 53, bib.V4Port, []byte("a")))
	clk.add(4 * time.Minute)
	e.Sweep()
	if e.SessionCount() != 1 {
		t.Fatal("refreshed UDP session expired early")
	}

	clk.add(5*time.Minute + time.Second)
	e.Sweep()
	if e.SessionCount() != 0 || e.BIBCount() != 0 {
		t.Fatalf("UDP timeout did not cascade: sessions=%d bib=%d",
			e.SessionCount(), e.BIBCount())
	}
}

// A BIB shared by two sessions survives the first session's timeout and is
// only removed once the reference count reaches zero.
func TestBIBRefCountCascade(t *testing.T) {
	e, clk := newTestEngine(t)
	src := mustV6(client6.String())
	dst1 := mustV6(e.synthV6(server4).String())
	dst2 := mustV6(e.synthV6(other4).String())

	e.HandleV6(buildUDP6(src, dst1, 41000, 53, []byte("1")))
	e.HandleV6(buildUDP6(src, dst2, 41000, 53, []byte("2")))
	bib := e.lookupBIBV6(ProtoUDP, client6, 41000)
	if bib == nil || bib.Refs != 2 {
		t.Fatalf("refs = %v, want 2", bib)
	}

	// Force one of the two sessions to expire.
	e.sessV6[V6SessionKey{Src: client6, Dst: e.synthV6(server4), Proto: ProtoUDP, SPort: 41000, DPort: 53}].
		ExpiresAt = clk.t.Add(-time.Second)
	e.Sweep()

	if e.SessionCount() != 1 || e.BIBCount() != 1 {
		t.Fatalf("BIB must outlive the first session: sessions=%d bib=%d",
			e.SessionCount(), e.BIBCount())
	}

	clk.add(6 * time.Minute)
	e.Sweep()
	if e.SessionCount() != 0 || e.BIBCount() != 0 {
		t.Fatalf("ref-zero cascade missing: sessions=%d bib=%d",
			e.SessionCount(), e.BIBCount())
	}
}

// TCP transitory states use the 4-minute timer and established connections
// the 2-hour timer; RST tears the session down immediately.
func TestTCPTimersAndRST(t *testing.T) {
	e, clk := newTestEngine(t)
	src := mustV6(client6.String())
	dst := mustV6(e.synthV6(server4).String())

	e.HandleV6(buildTCP6(src, dst, 42000, 443, tcpSYN, nil))
	s := e.sessV6[V6SessionKey{Src: client6, Dst: e.synthV6(server4), Proto: ProtoTCP, SPort: 42000, DPort: 443}]
	if s.State != StateV6Init {
		t.Fatalf("initial state = %v", s.State)
	}

	clk.add(3 * time.Minute)
	// SYN-ACK from the server establishes the connection (V4 side).
	bib := e.lookupBIBV6(ProtoTCP, client6, 42000)
	e.HandleV4(buildTCP4(server4.As4(), bib.V4Addr.As4(), 443, bib.V4Port, tcpSYN|tcpACK, nil))
	if s.State != StateV6Established {
		t.Fatalf("after SYNACK state = %v", s.State)
	}

	// Transitory 4-minute timer cannot reap an established connection; the
	// 2-hour timer can.
	clk.add(4 * time.Minute)
	e.Sweep()
	if e.SessionCount() != 1 {
		t.Fatal("established session reaped by transitory timer")
	}
	clk.add(2 * time.Hour)
	e.Sweep()
	if e.SessionCount() != 0 || e.BIBCount() != 0 {
		t.Fatalf("2h established timer failed: sessions=%d bib=%d",
			e.SessionCount(), e.BIBCount())
	}

	// Fresh connection: RST deletes state immediately.
	e.HandleV6(buildTCP6(src, dst, 42001, 443, tcpSYN, nil))
	e.HandleV4(buildTCP4(server4.As4(), e.lookupBIBV6(ProtoTCP, client6, 42001).V4Addr.As4(),
		443, e.lookupBIBV6(ProtoTCP, client6, 42001).V4Port, tcpRST, nil))
	if e.SessionCount() != 0 || e.BIBCount() != 0 {
		t.Fatalf("RST teardown failed: sessions=%d bib=%d", e.SessionCount(), e.BIBCount())
	}
}

// FIN drives the transitory states and the final ACK returns the flow to
// the established state on the opposite side.
func TestTCPFinStateMachine(t *testing.T) {
	e, _ := newTestEngine(t)
	src := mustV6(client6.String())
	dst := mustV6(e.synthV6(server4).String())

	e.HandleV6(buildTCP6(src, dst, 43000, 443, tcpSYN, nil))
	bib := e.lookupBIBV6(ProtoTCP, client6, 43000)
	e.HandleV4(buildTCP4(server4.As4(), bib.V4Addr.As4(), 443, bib.V4Port, tcpSYN|tcpACK, nil))
	e.HandleV6(buildTCP6(src, dst, 43000, 443, tcpACK, nil))

	key := V6SessionKey{Src: client6, Dst: e.synthV6(server4), Proto: ProtoTCP, SPort: 43000, DPort: 443}
	s := e.sessV6[key]

	e.HandleV6(buildTCP6(src, dst, 43000, 443, tcpFIN|tcpACK, nil))
	if s.State != StateV6Trans {
		t.Fatalf("after v6 FIN state = %v", s.State)
	}
	e.HandleV4(buildTCP4(server4.As4(), bib.V4Addr.As4(), 443, bib.V4Port, tcpACK, nil))
	if s.State != StateV4Established {
		t.Fatalf("after v4 ACK state = %v", s.State)
	}
	e.HandleV4(buildTCP4(server4.As4(), bib.V4Addr.As4(), 443, bib.V4Port, tcpFIN|tcpACK, nil))
	if s.State != StateV4Trans {
		t.Fatalf("after v4 FIN state = %v", s.State)
	}
	e.HandleV6(buildTCP6(src, dst, 43000, 443, tcpACK, nil))
	if s.State != StateV6Established {
		t.Fatalf("after v6 final ACK state = %v", s.State)
	}

	// Transitory 4-minute timer applies during half-close.
	s.State = StateV6Trans
	s.ExpiresAt = e.now().Add(-time.Second)
	e.Sweep()
	if e.SessionCount() != 0 {
		t.Fatal("half-closed session must be reaped by the 4m timer")
	}
}

// ICMP query sessions use the 60-second timer and the identifier pseudo-port.
func TestICMPTimeout(t *testing.T) {
	e, clk := newTestEngine(t)

	icmp6 := make([]byte, icmpHeader+4)
	icmp6[icmpType] = icmp6EchoRequest
	binary.BigEndian.PutUint16(icmp6[icmpID:], 0x1234)
	binary.BigEndian.PutUint16(icmp6[icmpSeq:], 1)
	binary.BigEndian.PutUint16(icmp6[icmpChecksum:],
		transportChecksumV6(mustV6(client6.String()), mustV6(e.synthV6(server4).String()), icmpv6Proto, icmp6))

	e.HandleV6(buildIPv6(0, icmpv6Proto, 64,
		mustV6(client6.String()), mustV6(e.synthV6(server4).String()), icmp6))
	if e.BIBCount() != 1 || e.SessionCount() != 1 {
		t.Fatal("ICMP echo did not create BIB/session")
	}

	clk.add(59 * time.Second)
	e.Sweep()
	if e.SessionCount() != 1 {
		t.Fatal("ICMP session expired before 60s")
	}
	clk.add(2 * time.Second)
	e.Sweep()
	if e.SessionCount() != 0 || e.BIBCount() != 0 {
		t.Fatalf("ICMP 60s timeout failed: sessions=%d bib=%d",
			e.SessionCount(), e.BIBCount())
	}
}
