package cid

import (
	"encoding/binary"
	"errors"
	"net"
	"testing"
)

func TestAuthenticationFailureDoesNotMovePeerOrSendProbe(t *testing.T) {
	oldAddr := udpAddr("192.0.2.10", 1000)
	newAddr := udpAddr("198.51.100.20", 2000)
	session := NewMemorySession([]byte("cid1"), oldAddr)
	packet := testRecord([]byte("cid1"), 1, 0, 16)

	verifyCalls := 0
	authErr := errors.New("bad tag")
	probeCalls := 0
	sendCalls := 0
	router := NewRouter(Config{
		Lookup: lookupSession(session),
		Verify: func(got Session, record Record) error {
			verifyCalls++
			if got != session {
				t.Fatal("verifier received the wrong session")
			}
			return authErr
		},
		Send: func(got Session, addr net.Addr, payload []byte) (int, error) {
			sendCalls++
			return len(payload), nil
		},
		Probe: func(got Session, addr net.Addr) ([]byte, error) {
			probeCalls++
			return []byte("probe"), nil
		},
	})

	_, err := router.Receive(packet, newAddr)
	if !errors.Is(err, ErrAuthentication) {
		t.Fatalf("Receive error = %v, want ErrAuthentication", err)
	}
	if verifyCalls != 1 {
		t.Fatalf("verify calls = %d, want 1", verifyCalls)
	}
	if !sameAddress(session.PeerAddress(), oldAddr) {
		t.Fatalf("peer moved to %v before authentication", session.PeerAddress())
	}
	if probeCalls != 0 || sendCalls != 0 {
		t.Fatalf("probe calls = %d, send calls = %d, want 0, 0", probeCalls, sendCalls)
	}
	if _, ok := router.PathStats(session); ok {
		t.Fatal("unauthenticated record created path state")
	}

	authErr = nil
	result, err := router.Receive(packet, newAddr)
	if err != nil {
		t.Fatalf("authenticated Receive error = %v", err)
	}
	if !result.Migrated {
		t.Fatal("authenticated record did not migrate the session")
	}
	if !sameAddress(session.PeerAddress(), newAddr) {
		t.Fatalf("peer = %v, want %v", session.PeerAddress(), newAddr)
	}
	if probeCalls != 1 || sendCalls != 1 {
		t.Fatalf("probe calls = %d, send calls = %d, want 1, 1", probeCalls, sendCalls)
	}
}

func TestReplayDoesNotMovePeerEvenAfterAuthentication(t *testing.T) {
	addrA := udpAddr("192.0.2.10", 1000)
	addrB := udpAddr("198.51.100.20", 2000)
	addrC := udpAddr("203.0.113.30", 3000)
	session := NewMemorySession([]byte("cid2"), addrA)

	probes := 0
	router := NewRouter(Config{
		Lookup: lookupSession(session),
		Verify: func(Session, Record) error { return nil },
		Send: func(Session, net.Addr, []byte) (int, error) {
			probes++
			return 1, nil
		},
		Probe: func(Session, net.Addr) ([]byte, error) { return []byte{0x01}, nil },
	})

	seen := testRecord([]byte("cid2"), 10, 0, 8)
	if _, err := router.Receive(seen, addrA); err != nil {
		t.Fatalf("first Receive error = %v", err)
	}
	if _, err := router.Receive(seen, addrB); !errors.Is(err, ErrReplay) {
		t.Fatalf("replay Receive error = %v, want ErrReplay", err)
	}
	if !sameAddress(session.PeerAddress(), addrA) {
		t.Fatalf("replay moved peer to %v", session.PeerAddress())
	}
	if probes != 0 {
		t.Fatalf("replay sent %d probes, want 0", probes)
	}

	fresh := testRecord([]byte("cid2"), 11, 0, 8)
	if _, err := router.Receive(fresh, addrB); err != nil {
		t.Fatalf("fresh Receive error = %v", err)
	}
	if !sameAddress(session.PeerAddress(), addrB) {
		t.Fatalf("fresh record did not move peer to B")
	}
	if probes != 1 {
		t.Fatalf("probes after migration = %d, want 1", probes)
	}

	if _, err := router.Receive(fresh, addrC); !errors.Is(err, ErrReplay) {
		t.Fatalf("replayed migration error = %v, want ErrReplay", err)
	}
	if !sameAddress(session.PeerAddress(), addrB) {
		t.Fatalf("replayed migration moved peer to %v", session.PeerAddress())
	}
	if probes != 1 {
		t.Fatalf("replayed migration sent another probe, total %d", probes)
	}
}

func TestAntiAmplificationLimitIncludesProbe(t *testing.T) {
	addrA := udpAddr("192.0.2.10", 1000)
	addrB := udpAddr("198.51.100.20", 2000)
	session := NewMemorySession([]byte("cid3"), addrA)

	sendCalls := 0
	router := NewRouter(Config{
		Lookup: lookupSession(session),
		Verify: func(Session, Record) error { return nil },
		Send: func(_ Session, _ net.Addr, payload []byte) (int, error) {
			sendCalls++
			return len(payload), nil
		},
		Probe: func(Session, net.Addr) ([]byte, error) { return make([]byte, 8), nil },
	})

	first := testRecordSized([]byte("cid3"), 1, 0, 100)
	result, err := router.Receive(first, addrB)
	if err != nil {
		t.Fatalf("Receive error = %v", err)
	}
	if !result.Migrated {
		t.Fatal("record did not migrate")
	}
	if result.ProbeBytes != 8 {
		t.Fatalf("probe bytes = %d, want 8", result.ProbeBytes)
	}

	stats, ok := router.PathStats(session)
	if !ok {
		t.Fatal("missing path stats after migration")
	}
	if stats.Received != 100 || stats.Sent != 8 {
		t.Fatalf("stats = received %d, sent %d; want 100, 8", stats.Received, stats.Sent)
	}
	callsBeforeReplay := sendCalls
	if _, err := router.Receive(first, addrB); !errors.Is(err, ErrReplay) {
		t.Fatalf("replay error = %v, want ErrReplay", err)
	}
	stats, _ = router.PathStats(session)
	if stats.Received != 100 || stats.Sent != 8 || sendCalls != callsBeforeReplay {
		t.Fatal("replayed record changed the amplification budget")
	}

	written, err := router.SendTo(session, addrB, make([]byte, 292))
	if err != nil {
		t.Fatalf("send up to exact limit error = %v", err)
	}
	if written != 292 {
		t.Fatalf("written = %d, want 292", written)
	}
	stats, _ = router.PathStats(session)
	if stats.Sent != 300 {
		t.Fatalf("sent = %d, want 300 (3 * 100)", stats.Sent)
	}

	callsBefore := sendCalls
	written, err = router.SendTo(session, addrB, make([]byte, 1))
	if !errors.Is(err, ErrAmplificationLimit) {
		t.Fatalf("over-limit send error = %v, want ErrAmplificationLimit", err)
	}
	if written != 0 {
		t.Fatalf("over-limit send wrote %d bytes", written)
	}
	if sendCalls != callsBefore {
		t.Fatal("over-limit send reached the network callback")
	}

	second := testRecordSized([]byte("cid3"), 2, 0, 100)
	if _, err := router.Receive(second, addrB); err != nil {
		t.Fatalf("second Receive error = %v", err)
	}
	stats, _ = router.PathStats(session)
	if stats.Received != 200 {
		t.Fatalf("received = %d, want 200", stats.Received)
	}
	if _, err := router.SendTo(session, addrB, make([]byte, 300)); err != nil {
		t.Fatalf("send under expanded limit error = %v", err)
	}
	if _, err := router.SendTo(session, addrB, make([]byte, 1)); !errors.Is(err, ErrAmplificationLimit) {
		t.Fatalf("expanded over-limit send error = %v, want ErrAmplificationLimit", err)
	}

	if err := router.ConfirmPath(session, addrB); err != nil {
		t.Fatalf("ConfirmPath error = %v", err)
	}
	if _, err := router.SendTo(session, addrB, make([]byte, 4096)); err != nil {
		t.Fatalf("validated path send error = %v", err)
	}
}

func TestCIDLengthMismatchIsDroppedBeforeAuthentication(t *testing.T) {
	addrA := udpAddr("192.0.2.10", 1000)
	addrB := udpAddr("198.51.100.20", 2000)
	session := NewMemorySession([]byte("cid4"), addrA)

	verifyCalls := 0
	probeCalls := 0
	router := NewRouter(Config{
		Lookup: func([]byte) (Session, bool) { return session, true },
		Verify: func(Session, Record) error {
			verifyCalls++
			return nil
		},
		Send: func(Session, net.Addr, []byte) (int, error) {
			t.Fatal("send called for CID length mismatch")
			return 0, nil
		},
		Probe: func(Session, net.Addr) ([]byte, error) {
			probeCalls++
			return []byte{1}, nil
		},
	})

	wrongLength := testRecord([]byte("cid"), 1, 0, 8)
	if _, err := router.Receive(wrongLength, addrB); !errors.Is(err, ErrCIDLengthMismatch) {
		t.Fatalf("wrong-length CID error = %v, want ErrCIDLengthMismatch", err)
	}
	if verifyCalls != 0 || probeCalls != 0 {
		t.Fatalf("verify calls = %d, probe calls = %d, want 0, 0", verifyCalls, probeCalls)
	}
	if !sameAddress(session.PeerAddress(), addrA) {
		t.Fatalf("wrong-length CID moved peer to %v", session.PeerAddress())
	}

	wrongValue := testRecord([]byte("xxxx"), 2, 0, 8)
	if _, err := router.Receive(wrongValue, addrB); !errors.Is(err, ErrUnknownCID) {
		t.Fatalf("wrong CID value error = %v, want ErrUnknownCID", err)
	}
	if verifyCalls != 0 {
		t.Fatal("wrong CID value reached authentication")
	}

	truncated := testRecord([]byte("cid4"), 3, 0, 8)
	truncated[11] = 5
	truncated = truncated[:len(truncated)-1]
	if _, err := router.Receive(truncated, addrB); !errors.Is(err, ErrMalformedRecord) {
		t.Fatalf("truncated CID error = %v, want ErrMalformedRecord", err)
	}
	if verifyCalls != 0 {
		t.Fatal("malformed CID reached authentication")
	}
}

func TestOnlyTLS12CIDRecordsAreRouted(t *testing.T) {
	session := NewMemorySession([]byte("cid5"), udpAddr("192.0.2.10", 1000))
	lookups := 0
	router := NewRouter(Config{
		Lookup: func([]byte) (Session, bool) {
			lookups++
			return session, true
		},
		Verify: func(Session, Record) error { return nil },
	})

	plain := make([]byte, dtlsRecordHeaderLen)
	plain[0] = 23
	binary.BigEndian.PutUint16(plain[1:3], 0xfefd)
	if _, err := router.Receive(plain, session.PeerAddress()); !errors.Is(err, ErrNotCID) {
		t.Fatalf("plain record error = %v, want ErrNotCID", err)
	}
	if lookups != 0 {
		t.Fatal("non-CID record reached session lookup")
	}
}

func TestOneProbePerAddressChange(t *testing.T) {
	addrA := udpAddr("192.0.2.10", 1000)
	addrB := udpAddr("198.51.100.20", 2000)
	addrC := udpAddr("203.0.113.30", 3000)
	session := NewMemorySession([]byte("cid6"), addrA)

	var probeAddrs []string
	router := NewRouter(Config{
		Lookup: lookupSession(session),
		Verify: func(Session, Record) error { return nil },
		Send: func(_ Session, addr net.Addr, payload []byte) (int, error) {
			return len(payload), nil
		},
		Probe: func(_ Session, addr net.Addr) ([]byte, error) {
			probeAddrs = append(probeAddrs, addr.String())
			return []byte{1, 2, 3, 4}, nil
		},
	})

	if _, err := router.Receive(testRecord([]byte("cid6"), 1, 0, 32), addrB); err != nil {
		t.Fatalf("migration to B error = %v", err)
	}
	if _, err := router.Receive(testRecord([]byte("cid6"), 2, 0, 32), addrB); err != nil {
		t.Fatalf("second record from B error = %v", err)
	}
	if len(probeAddrs) != 1 || probeAddrs[0] != addrB.String() {
		t.Fatalf("probes after B = %v, want [%s]", probeAddrs, addrB)
	}

	if err := router.ConfirmPath(session, addrB); err != nil {
		t.Fatalf("ConfirmPath B error = %v", err)
	}
	if _, err := router.Receive(testRecord([]byte("cid6"), 3, 0, 32), addrC); err != nil {
		t.Fatalf("migration to C error = %v", err)
	}
	if len(probeAddrs) != 2 || probeAddrs[1] != addrC.String() {
		t.Fatalf("probes after C = %v, want second probe to %s", probeAddrs, addrC)
	}
}

func TestReplayWindowBoundaries(t *testing.T) {
	var window ReplayWindow
	if !window.Accept(100) {
		t.Fatal("first sequence rejected")
	}
	if window.Accept(100) {
		t.Fatal("duplicate sequence accepted")
	}
	if !window.Accept(37) {
		t.Fatal("sequence at window boundary rejected")
	}
	if window.Accept(36) {
		t.Fatal("sequence outside window accepted")
	}
	if !window.Accept(200) {
		t.Fatal("new highest sequence rejected")
	}
	if !window.Accept(137) {
		t.Fatal("oldest in-window sequence rejected")
	}
	if window.Accept(136) {
		t.Fatal("sequence one past window accepted")
	}
	if !window.Seen(137) || window.Seen(136) {
		t.Fatal("Seen does not match accepted window")
	}
}

func lookupSession(session Session) LookupFunc {
	return func(connectionID []byte) (Session, bool) {
		if string(connectionID) != string(session.ConnectionID()) {
			return nil, false
		}
		return session, true
	}
}

func udpAddr(host string, port int) *net.UDPAddr {
	return &net.UDPAddr{IP: net.ParseIP(host), Port: port}
}

func testRecord(cid []byte, sequence uint64, epoch uint16, fragmentLength int) []byte {
	headerLength := 14 + len(cid)
	return testRecordWithFragment(cid, sequence, epoch, make([]byte, fragmentLength), headerLength)
}

func testRecordSized(cid []byte, sequence uint64, epoch uint16, packetLength int) []byte {
	headerLength := 14 + len(cid)
	if packetLength < headerLength {
		panic("packet length is smaller than record header")
	}
	return testRecord(cid, sequence, epoch, packetLength-headerLength)
}

func testRecordWithFragment(cid []byte, sequence uint64, epoch uint16, fragment []byte, headerLength int) []byte {
	packet := make([]byte, headerLength+len(fragment))
	packet[0] = ContentTypeTLS12CID
	binary.BigEndian.PutUint16(packet[1:3], 0xfefd)
	binary.BigEndian.PutUint16(packet[3:5], epoch)
	packet[5] = byte(sequence >> 40)
	packet[6] = byte(sequence >> 32)
	packet[7] = byte(sequence >> 24)
	packet[8] = byte(sequence >> 16)
	packet[9] = byte(sequence >> 8)
	packet[10] = byte(sequence)
	packet[11] = byte(len(cid))
	copy(packet[12:], cid)
	binary.BigEndian.PutUint16(packet[headerLength-2:headerLength], uint16(len(fragment)))
	copy(packet[headerLength:], fragment)
	return packet
}
