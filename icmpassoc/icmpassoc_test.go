package icmpassoc

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"testing"
)

// ---- crafted packet builders ---------------------------------------------

func buildIPv4(proto uint8, src, dst [4]byte, options, transport []byte) []byte {
	ihl := 20 + len(options)
	total := ihl + len(transport)
	b := make([]byte, total)
	b[0] = 0x40 | byte(ihl/4)
	binary.BigEndian.PutUint16(b[2:4], uint16(total))
	b[8] = 64
	b[9] = proto
	copy(b[12:16], src[:])
	copy(b[16:20], dst[:])
	copy(b[20:], options)
	copy(b[ihl:], transport)
	return b
}

func buildIPv6(proto uint8, src, dst [16]byte, transport []byte) []byte {
	b := make([]byte, 40+len(transport))
	b[0] = 0x60
	binary.BigEndian.PutUint16(b[4:6], uint16(len(transport)))
	b[6] = proto
	b[7] = 64
	copy(b[8:24], src[:])
	copy(b[24:40], dst[:])
	copy(b[40:], transport)
	return b
}

func tcpPorts(sport, dport uint16, seq uint32) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint16(b[0:2], sport)
	binary.BigEndian.PutUint16(b[2:4], dport)
	binary.BigEndian.PutUint32(b[4:8], seq)
	return b
}

func udpPorts(sport, dport uint16) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint16(b[0:2], sport)
	binary.BigEndian.PutUint16(b[2:4], dport)
	binary.BigEndian.PutUint16(b[4:6], 8)
	return b
}

// ---- test helpers --------------------------------------------------------

type recorder struct{ notices []Notice }

func (r *recorder) notify(n Notice) { r.notices = append(r.notices, n) }

func (r *recorder) count() int { return len(r.notices) }

var (
	v4Local  = [4]byte{10, 0, 0, 2}
	v4Remote = [4]byte{198, 51, 100, 7}
	v6Local  = [16]byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x02}
	v6Remote = [16]byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x07}
)

func v4Flow(proto Protocol) FlowKey {
	return FlowKey{
		Version:  Version4,
		Protocol: proto,
		Local:    Endpoint{Version4, netip.MustParseAddr("10.0.0.2"), 4000},
		Remote:   Endpoint{Version4, netip.MustParseAddr("198.51.100.7"), 80},
	}
}

func v6Flow(proto Protocol) FlowKey {
	return FlowKey{
		Version:  Version6,
		Protocol: proto,
		Local:    Endpoint{Version6, netip.MustParseAddr("2001:db8::2"), 4000},
		Remote:   Endpoint{Version6, netip.MustParseAddr("2001:db8::7"), 80},
	}
}

func assertNotAssociated(t *testing.T, res Result, want Reason) {
	t.Helper()
	if res.Status != StatusNotAssociated {
		t.Fatalf("status = %v, want not-associated", res.Status)
	}
	if res.Reason != want {
		t.Fatalf("reason = %q, want %q", res.Reason, want)
	}
	if !errors.Is(res.Error(), ErrCannotAssociate) {
		t.Fatalf("error %v does not wrap ErrCannotAssociate", res.Error())
	}
}

func mustIdentity(t *testing.T, v IPVersion) AddrMapper {
	t.Helper()
	m, err := NewIdentityMapper(v)
	if err != nil {
		t.Fatalf("identity mapper: %v", err)
	}
	return m
}

// ---- short / truncated quotes --------------------------------------------

func TestShortQuotes(t *testing.T) {
	host := NewHost(WithMapper(mustIdentity(t, Version4)))

	// No bytes at all.
	assertNotAssociated(t, host.HandleICMPError(3, 3, nil), ReasonTruncated)

	// IPv4 header one byte short of the 20-byte minimum.
	assertNotAssociated(t, host.HandleICMPError(3, 3, make([]byte, 19)), ReasonTruncated)

	// Complete header but IHL claims more bytes than the quote contains.
	badIHL := buildIPv4(uint8(ProtoTCP), v4Local, v4Remote, nil, nil)
	badIHL[0] = 0x40 | 10 // IHL = 40 bytes, only 20 present
	assertNotAssociated(t, host.HandleICMPError(3, 3, badIHL), ReasonBadHeader)

	// TCP needs 8 transport bytes: a 6-byte quote must not associate.
	shortTCP := buildIPv4(uint8(ProtoTCP), v4Local, v4Remote, nil, tcpPorts(4000, 80, 1)[:6])
	assertNotAssociated(t, host.HandleICMPError(3, 3, shortTCP), ReasonTruncated)

	// UDP needs the 4 port bytes: a 3-byte quote must not associate.
	shortUDP := buildIPv4(uint8(ProtoUDP), v4Local, v4Remote, nil, udpPorts(4000, 80)[:3])
	assertNotAssociated(t, host.HandleICMPError(3, 3, shortUDP), ReasonTruncated)

	// Unsupported transport protocol (SCTP).
	sctp := buildIPv4(132, v4Local, v4Remote, nil, make([]byte, 12))
	assertNotAssociated(t, host.HandleICMPError(3, 3, sctp), ReasonUnsupportedProto)

	// Unknown IP version nibble.
	badVersion := buildIPv4(uint8(ProtoTCP), v4Local, v4Remote, nil, tcpPorts(4000, 80, 1))
	badVersion[0] = 0x90
	assertNotAssociated(t, host.HandleICMPError(3, 3, badVersion), ReasonUnsupportedIP)
}

// ---- IPv4 options are skipped when locating the transport header ---------

func TestIPv4OptionsHeader(t *testing.T) {
	rec := &recorder{}
	host := NewHost(WithMapper(mustIdentity(t, Version4)))
	conn, err := host.Register(v4Flow(ProtoTCP), rec.notify)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := conn.SetSeqWindow(SeqWindow{Base: 1000, Length: 100}); err != nil {
		t.Fatalf("window: %v", err)
	}

	// 12 bytes of options (NOP, NOP, then a 10-byte timestamp option).
	options := []byte{1, 1, 68, 10, 0, 0, 0, 0, 0, 0, 0, 0}
	pkt := buildIPv4(uint8(ProtoTCP), v4Local, v4Remote, options, tcpPorts(4000, 80, 1050))

	res := host.HandleICMPError(3, 4, pkt)
	if res.Status != StatusDelivered || rec.count() != 1 {
		t.Fatalf("delivery failed: %+v notices=%d", res, rec.count())
	}
	got := rec.notices[0]
	if got.ICMPType != 3 || got.ICMPCode != 4 {
		t.Fatalf("type/code = %d/%d, want 3/4", got.ICMPType, got.ICMPCode)
	}
	if got.QuotedLen != 8 || got.TCPSeq != 1050 || !got.HasTCPSeq {
		t.Fatalf("notice transport fields wrong: %+v", got)
	}

	// Options present but transport truncated: still unassociable.
	trunc := buildIPv4(uint8(ProtoTCP), v4Local, v4Remote, options, tcpPorts(4000, 80, 1050)[:7])
	assertNotAssociated(t, host.HandleICMPError(3, 4, trunc), ReasonTruncated)
}

// ---- TCP sequence window crossing the 2^32 wrap point --------------------

func TestWraparoundWindow(t *testing.T) {
	rec := &recorder{}
	host := NewHost(WithMapper(mustIdentity(t, Version4)))
	conn, err := host.Register(v4Flow(ProtoTCP), rec.notify)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// Window [0xFFFFFFFA, +10) wraps through zero to 0x00000003.
	window := SeqWindow{Base: 0xFFFFFFFA, Length: 10}
	if err := conn.SetSeqWindow(window); err != nil {
		t.Fatalf("window: %v", err)
	}

	cases := []struct {
		name string
		seq  uint32
		want bool
	}{
		{"first base value", 0xFFFFFFFA, true},
		{"last pre-wrap value", 0xFFFFFFFF, true},
		{"first post-wrap value", 0x00000000, true},
		{"last in-window value", 0x00000003, true},
		{"just after window", 0x00000004, false},
		{"just before window", 0xFFFFFFF9, false},
		{"mid-space far side", 0x80000000, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := rec.count()
			pkt := buildIPv4(uint8(ProtoTCP), v4Local, v4Remote, nil, tcpPorts(4000, 80, tc.seq))
			res := host.HandleICMPError(11, 0, pkt)
			if tc.want {
				if res.Status != StatusDelivered || rec.count() != before+1 {
					t.Fatalf("seq %#x: want delivery, got %+v", tc.seq, res)
				}
				last := rec.notices[len(rec.notices)-1]
				if last.TCPSeq != tc.seq {
					t.Fatalf("notice seq = %#x, want %#x", last.TCPSeq, tc.seq)
				}
			} else {
				assertNotAssociated(t, res, ReasonSeqOutside)
				if rec.count() != before {
					t.Fatalf("seq %#x: connection was notified despite miss", tc.seq)
				}
			}
		})
	}

	// A TCP connection without a window never accepts an error.
	rec2 := &recorder{}
	conn2, err := host.Register(FlowKey{
		Version:  Version4,
		Protocol: ProtoTCP,
		Local:    Endpoint{Version4, netip.MustParseAddr("10.0.0.3"), 5000},
		Remote:   Endpoint{Version4, netip.MustParseAddr("198.51.100.8"), 443},
	}, rec2.notify)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	pkt := buildIPv4(uint8(ProtoTCP),
		[4]byte{10, 0, 0, 3}, [4]byte{198, 51, 100, 8},
		nil, tcpPorts(5000, 443, 7))
	assertNotAssociated(t, host.HandleICMPError(3, 3, pkt), ReasonNoWindow)
	if rec2.count() != 0 {
		t.Fatal("connection without window was notified")
	}
	_ = conn2
}

// ---- closing a tuple and reusing its four-tuple gets a new generation ----

func TestCloseThenReuseTuple(t *testing.T) {
	host := NewHost(WithMapper(mustIdentity(t, Version4)))

	gen1Rec := &recorder{}
	conn1, err := host.Register(v4Flow(ProtoTCP), gen1Rec.notify)
	if err != nil {
		t.Fatalf("register gen1: %v", err)
	}
	if conn1.Generation() != 1 {
		t.Fatalf("first generation = %d, want 1", conn1.Generation())
	}
	if err := conn1.SetSeqWindow(SeqWindow{Base: 100, Length: 50}); err != nil {
		t.Fatalf("window: %v", err)
	}

	pkt := func(seq uint32) []byte {
		return buildIPv4(uint8(ProtoTCP), v4Local, v4Remote, nil, tcpPorts(4000, 80, seq))
	}

	// In-window error reaches generation 1.
	if res := host.HandleICMPError(3, 3, pkt(110)); res.Status != StatusDelivered {
		t.Fatalf("gen1 delivery: %+v", res)
	}

	// Duplicate active registration is rejected.
	if _, err := host.Register(v4Flow(ProtoTCP), gen1Rec.notify); !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("duplicate register err = %v, want ErrAlreadyRegistered", err)
	}

	// Retire generation 1.
	if err := conn1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// The same forged quote must not reach the closed generation.
	assertNotAssociated(t, host.HandleICMPError(3, 3, pkt(110)), ReasonGeneration)
	if gen1Rec.count() != 1 {
		t.Fatalf("closed generation got %d notices, want 1", gen1Rec.count())
	}

	// Reuse the identical four-tuple: generation 2 starts fresh.
	gen2Rec := &recorder{}
	conn2, err := host.Register(v4Flow(ProtoTCP), gen2Rec.notify)
	if err != nil {
		t.Fatalf("register gen2: %v", err)
	}
	if conn2.Generation() != 2 {
		t.Fatalf("second generation = %d, want 2", conn2.Generation())
	}
	if err := conn2.SetSeqWindow(SeqWindow{Base: 9000, Length: 10}); err != nil {
		t.Fatalf("window gen2: %v", err)
	}

	// A quote matching gen1's old window must not touch gen2.
	assertNotAssociated(t, host.HandleICMPError(3, 3, pkt(110)), ReasonSeqOutside)
	if gen2Rec.count() != 0 {
		t.Fatal("new generation notified by stale-window error")
	}

	// A quote inside gen2's window is delivered only to generation 2.
	res := host.HandleICMPError(3, 3, pkt(9005))
	if res.Status != StatusDelivered || res.Generation != 2 || gen2Rec.count() != 1 {
		t.Fatalf("gen2 delivery: %+v notices=%d", res, gen2Rec.count())
	}
	if gen1Rec.count() != 1 {
		t.Fatal("closed generation received a late notice")
	}

	// Closing an already-closed handle is an error.
	if err := conn1.Close(); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("double close err = %v, want ErrNotRegistered", err)
	}
}

// ---- caller-supplied fixed-version translation ---------------------------

func TestStaticMapperTranslationAndVersionLock(t *testing.T) {
	// The quoted packet uses external (post-NAT) addresses/ports; the host
	// stores the internal tuple. Only the fixed IPv4 mapper bridges them.
	rules := map[Endpoint]Endpoint{
		{Version4, netip.MustParseAddr("203.0.113.9"), 5000}: {Version4, netip.MustParseAddr("10.0.0.2"), 4000},
		{Version4, netip.MustParseAddr("198.51.100.7"), 80}:  {Version4, netip.MustParseAddr("198.51.100.7"), 80},
	}
	mapper, err := NewStaticMapper(Version4, rules)
	if err != nil {
		t.Fatalf("static mapper: %v", err)
	}
	host := NewHost(WithMapper(mapper))

	rec := &recorder{}
	conn, err := host.Register(v4Flow(ProtoTCP), rec.notify)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := conn.SetSeqWindow(SeqWindow{Base: 500, Length: 10}); err != nil {
		t.Fatalf("window: %v", err)
	}

	external := buildIPv4(uint8(ProtoTCP),
		[4]byte{203, 0, 113, 9}, v4Remote,
		nil, tcpPorts(5000, 80, 505))
	res := host.HandleICMPError(3, 10, external)
	if res.Status != StatusDelivered || rec.count() != 1 {
		t.Fatalf("translated delivery failed: %+v", res)
	}

	// Flip one port: translation no longer lands on the registered tuple.
	wrongPort := buildIPv4(uint8(ProtoTCP),
		[4]byte{203, 0, 113, 9}, v4Remote,
		nil, tcpPorts(5001, 80, 505))
	assertNotAssociated(t, host.HandleICMPError(3, 10, wrongPort), ReasonNoConn)

	// The IPv4 mapper must reject IPv6 endpoints, and an IPv6 datagram with
	// no v6 mapper is unassociable rather than crossing families.
	if _, err := mapper.Translate(Endpoint{Version6, netip.MustParseAddr("2001:db8::1"), 1}); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("cross-family translate err = %v, want ErrVersionMismatch", err)
	}
	v6pkt := buildIPv6(uint8(ProtoTCP), v6Local, v6Remote, tcpPorts(4000, 80, 1))
	assertNotAssociated(t, host.HandleICMPError(1, 0, v6pkt), ReasonNoMapper)

	// Building a rule from the wrong family is rejected up front.
	badRules := map[Endpoint]Endpoint{
		{Version4, netip.MustParseAddr("10.0.0.2"), 1}: {Version6, netip.MustParseAddr("2001:db8::2"), 1},
	}
	if _, err := NewStaticMapper(Version4, badRules); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("mixed-family construction err = %v, want ErrVersionMismatch", err)
	}
}

// ---- UDP demultiplexing and IPv6 plumbing --------------------------------

func TestUDPAndIPv6(t *testing.T) {
	v4Mapper, err := NewIdentityMapper(Version4)
	if err != nil {
		t.Fatal(err)
	}
	v6Mapper, err := NewIdentityMapper(Version6)
	if err != nil {
		t.Fatal(err)
	}
	host := NewHost(WithMapper(v4Mapper), WithMapper(v6Mapper))

	udpRec := &recorder{}
	if _, err := host.Register(v4Flow(ProtoUDP), udpRec.notify); err != nil {
		t.Fatalf("register udp: %v", err)
	}
	udpPkt := buildIPv4(uint8(ProtoUDP), v4Local, v4Remote, nil, udpPorts(4000, 80))
	res := host.HandleICMPError(3, 3, udpPkt)
	if res.Status != StatusDelivered || udpRec.count() != 1 {
		t.Fatalf("udp delivery: %+v", res)
	}
	if udpRec.notices[0].HasTCPSeq {
		t.Fatal("UDP notice must not carry a TCP sequence")
	}

	tcp6Rec := &recorder{}
	conn6, err := host.Register(v6Flow(ProtoTCP), tcp6Rec.notify)
	if err != nil {
		t.Fatalf("register tcp6: %v", err)
	}
	if err := conn6.SetSeqWindow(SeqWindow{Base: 77, Length: 3}); err != nil {
		t.Fatalf("window: %v", err)
	}
	v6pkt := buildIPv6(uint8(ProtoTCP), v6Local, v6Remote, tcpPorts(4000, 80, 78))
	if res := host.HandleICMPError(1, 0, v6pkt); res.Status != StatusDelivered {
		t.Fatalf("ipv6 delivery: %+v", res)
	}

	// IPv6 extension headers (fragment header = 44) are refused explicitly.
	ext := buildIPv6(44, v6Local, v6Remote, make([]byte, 16))
	assertNotAssociated(t, host.HandleICMPError(1, 0, ext), ReasonExtHeader)

	// Truncated IPv6 fixed header.
	truncV6 := make([]byte, 39)
	truncV6[0] = 0x60
	assertNotAssociated(t, host.HandleICMPError(1, 0, truncV6), ReasonTruncated)
}

func TestWindowValidation(t *testing.T) {
	if (SeqWindow{}).Valid() {
		t.Fatal("zero-length window must be invalid")
	}
	if (SeqWindow{Base: 1, Length: MaxWindowLength + 1}).Valid() {
		t.Fatal("over-large window must be invalid")
	}
	host := NewHost(WithMapper(mustIdentity(t, Version4)))
	conn, err := host.Register(v4Flow(ProtoTCP), (&recorder{}).notify)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.SetSeqWindow(SeqWindow{Base: 1, Length: 0}); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("invalid window err = %v, want ErrInvalidWindow", err)
	}
}

// ---- tuple mismatch and API guard rails ----------------------------------

func TestMismatchAndGuardRails(t *testing.T) {
	host := NewHost(WithMapper(mustIdentity(t, Version4)))
	rec := &recorder{}
	conn, err := host.Register(v4Flow(ProtoTCP), rec.notify)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.SetSeqWindow(SeqWindow{Base: 100, Length: 10}); err != nil {
		t.Fatal(err)
	}

	match := func(proto uint8, src, dst [4]byte, sport, dport uint16, seq uint32) []byte {
		return buildIPv4(proto, src, dst, nil, tcpPorts(sport, dport, seq))
	}

	// Protocol mismatch: UDP packet against the TCP tuple.
	udpPkt := buildIPv4(uint8(ProtoUDP), v4Local, v4Remote, nil, udpPorts(4000, 80))
	assertNotAssociated(t, host.HandleICMPError(3, 3, udpPkt), ReasonNoConn)

	// Address mismatch.
	assertNotAssociated(t, host.HandleICMPError(3, 3,
		match(uint8(ProtoTCP), [4]byte{10, 0, 0, 9}, v4Remote, 4000, 80, 100)), ReasonNoConn)
	// Port mismatch.
	assertNotAssociated(t, host.HandleICMPError(3, 3,
		match(uint8(ProtoTCP), v4Local, v4Remote, 4001, 80, 100)), ReasonNoConn)

	// Nil notify and malformed flow are rejected at registration.
	if _, err := host.Register(v4Flow(ProtoTCP), nil); err == nil {
		t.Fatal("nil notify must fail")
	}
	badFlow := v4Flow(ProtoTCP)
	badFlow.Remote.Version = Version6
	if _, err := host.Register(badFlow, rec.notify); err == nil {
		t.Fatal("version-mixed flow must fail")
	}

	// Sequence windows only apply to TCP.
	udpConn, err := host.Register(v4Flow(ProtoUDP), rec.notify)
	if err != nil {
		t.Fatal(err)
	}
	if err := udpConn.SetSeqWindow(SeqWindow{Base: 1, Length: 2}); err == nil {
		t.Fatal("UDP must reject a sequence window")
	}

	// Closing a nil/unknown handle is reported cleanly.
	if err := host.Close(nil); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("nil close err = %v, want ErrNotRegistered", err)
	}

	// Mapper construction guard rails.
	if _, err := NewIdentityMapper(IPVersion(9)); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("bad identity version err = %v", err)
	}
	id4, _ := NewIdentityMapper(Version4)
	if _, err := id4.Translate(Endpoint{Version6, netip.MustParseAddr("::1"), 1}); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("identity cross-family err = %v", err)
	}
}

// ---- declared total length clips link-layer padding ----------------------

func TestIPv4TotalLengthPadding(t *testing.T) {
	host := NewHost(WithMapper(mustIdentity(t, Version4)))
	rec := &recorder{}
	conn, err := host.Register(v4Flow(ProtoUDP), rec.notify)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn

	pkt := buildIPv4(uint8(ProtoUDP), v4Local, v4Remote, nil, udpPorts(4000, 80))
	// Append Ethernet-style zero padding without updating total length.
	padded := append(pkt, make([]byte, 10)...)
	res := host.HandleICMPError(3, 3, padded)
	if res.Status != StatusDelivered || rec.count() != 1 {
		t.Fatalf("padded datagram delivery: %+v notices=%d", res, rec.count())
	}
	if got := rec.notices[0].QuotedLen; got != 8 {
		t.Fatalf("quoted transport length = %d, want 8 (padding clipped)", got)
	}
}

func TestConcurrentRegisterAndErrors(t *testing.T) {
	host := NewHost(WithMapper(mustIdentity(t, Version4)))
	pkt := buildIPv4(uint8(ProtoTCP), v4Local, v4Remote, nil, tcpPorts(4000, 80, 100))

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			host.HandleICMPError(3, 3, pkt)
		}
	}()

	for i := 0; i < 50; i++ {
		rec := &recorder{}
		conn, err := host.Register(v4Flow(ProtoTCP), rec.notify)
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		if err := conn.SetSeqWindow(SeqWindow{Base: 100, Length: 1}); err != nil {
			t.Fatalf("window: %v", err)
		}
		if err := conn.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}
	<-done
}
