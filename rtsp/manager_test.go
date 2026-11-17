package rtsp

import (
	"errors"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"testing"
)

// recordingSink records every destination the manager ever asks it to send
// to, so tests can assert no media reaches an attacker-controlled address.
type recordingSink struct {
	mu     sync.Mutex
	dsts   []netip.AddrPort
	packet [][]byte
}

func (s *recordingSink) SendRTP(dst netip.AddrPort, packet []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dsts = append(s.dsts, dst)
	s.packet = append(s.packet, append([]byte(nil), packet...))
	return nil
}

func mustAddr(t *testing.T, text string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return addr
}

func statusOf(t *testing.T, err error) int {
	t.Helper()
	var rtspErr *Error
	if !errors.As(err, &rtspErr) {
		t.Fatalf("expected *rtsp.Error, got %T: %v", err, err)
	}
	return rtspErr.Status
}

const validTransport = "RTP/AVP;unicast;client_port=5000-5001"

// TestSetupRejectsDestinationRewrite is the reflection attack: a Transport
// header naming a victim destination must be rejected while the real source
// address remains the only pinned target.
func TestSetupRejectsDestinationRewrite(t *testing.T) {
	mgr := NewManager()
	source := mustAddr(t, "203.0.113.10")
	victim := mustAddr(t, "198.51.100.20")

	_, err := mgr.Setup("alice", source,
		"RTP/AVP;unicast;client_port=5000-5001;destination="+victim.String(), 1)
	if statusOf(t, err) != StatusBadRequest {
		t.Fatalf("rewritten destination: want status %d, got %v", StatusBadRequest, err)
	}

	// An explicit destination equal to the source is acceptable.
	session, err := mgr.Setup("alice", source,
		"RTP/AVP;unicast;client_port=5000-5001;destination="+source.String(), 1)
	if err != nil {
		t.Fatalf("same-address destination rejected: %v", err)
	}

	// A header without destination defaults to the source and must also pin
	// there, never to any value supplied elsewhere.
	defaulted, err := mgr.Setup("alice", source, validTransport, 2)
	if err != nil {
		t.Fatalf("default destination rejected: %v", err)
	}

	wantPort := netip.AddrPortFrom(source, 5000)
	for _, s := range []*Session{session, defaulted} {
		if got := s.Target.rtpAddrPort(); got != wantPort {
			t.Fatalf("target pinned to %v, want %v", got, wantPort)
		}
	}
}

// TestForeignPrincipalRejected proves a session is usable only by its owner:
// another principal's PLAY and TEARDOWN get 454 and change nothing.
func TestForeignPrincipalRejected(t *testing.T) {
	mgr := NewManager()
	source := mustAddr(t, "203.0.113.10")

	owner, err := mgr.Setup("alice", source, validTransport, 1)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	if _, err := mgr.Play("mallory", owner.ID, 2); statusOf(t, err) != StatusSessionNotFound {
		t.Fatalf("foreign PLAY: want %d, got %v", StatusSessionNotFound, err)
	}
	if err := mgr.Teardown("mallory", owner.ID, 2); statusOf(t, err) != StatusSessionNotFound {
		t.Fatalf("foreign TEARDOWN: want %d, got %v", StatusSessionNotFound, err)
	}

	// An unknown id returns the identical 454, so ids cannot be enumerated.
	if _, err := mgr.Play("mallory", SessionID{}, 2); statusOf(t, err) != StatusSessionNotFound {
		t.Fatalf("unknown session: want %d, got %v", StatusSessionNotFound, err)
	}

	// The owner's PLAY still works: the foreign requests changed no state.
	if _, err := mgr.Play("alice", owner.ID, 2); err != nil {
		t.Fatalf("owner PLAY after foreign attempts: %v", err)
	}

	// Media still flows only to the owner's pinned address.
	sink := &recordingSink{}
	if err := mgr.Deliver(owner.ID, sink, []byte("rtp")); err != nil {
		t.Fatalf("owner delivery: %v", err)
	}
	if len(sink.dsts) != 1 || sink.dsts[0] != netip.AddrPortFrom(source, 5000) {
		t.Fatalf("media went to %v", sink.dsts)
	}
}

// TestCSeqMustIncrease rejects equal, smaller and zero CSeq values while
// accepting strictly larger ones.
func TestCSeqMustIncrease(t *testing.T) {
	mgr := NewManager()
	source := mustAddr(t, "203.0.113.10")

	if _, err := mgr.Setup("alice", source, validTransport, 0); statusOf(t, err) != StatusBadRequest {
		t.Fatalf("zero SETUP CSeq: want %d, got %v", StatusBadRequest, err)
	}

	session, err := mgr.Setup("alice", source, validTransport, 10)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	for _, cseq := range []uint32{10, 9, 0} {
		if _, err := mgr.Play("alice", session.ID, cseq); statusOf(t, err) != StatusBadRequest {
			t.Fatalf("CSeq %d after 10: want %d, got %v", cseq, StatusBadRequest, err)
		}
	}

	if _, err := mgr.Play("alice", session.ID, 11); err != nil {
		t.Fatalf("increasing PLAY CSeq: %v", err)
	}
	if err := mgr.Teardown("alice", session.ID, 11); statusOf(t, err) != StatusBadRequest {
		t.Fatalf("equal TEARDOWN CSeq: want %d, got %v", StatusBadRequest, err)
	}
	if err := mgr.Teardown("alice", session.ID, 12); err != nil {
		t.Fatalf("increasing TEARDOWN CSeq: %v", err)
	}

	// A torn-down session is gone for everyone, owner included.
	if _, err := mgr.Play("alice", session.ID, 13); statusOf(t, err) != StatusSessionNotFound {
		t.Fatalf("play after teardown: want %d, got %v", StatusSessionNotFound, err)
	}
}

// TestLegitimatePlaybackDeliversOnlyToOwner runs a full alice SETUP/PLAY
// while mallory tries every redirect and hijack, then asserts every emitted
// packet targets alice's pinned address and teardown stops delivery.
func TestLegitimatePlaybackDeliversOnlyToOwner(t *testing.T) {
	mgr := NewManager()
	aliceAddr := mustAddr(t, "203.0.113.10")
	malloryAddr := mustAddr(t, "198.51.100.66")
	victimAddr := mustAddr(t, "192.0.2.77")

	session, err := mgr.Setup("alice", aliceAddr, validTransport, 1)
	if err != nil {
		t.Fatalf("alice setup: %v", err)
	}

	// Mallory's reflected SETUP pointing destination at the victim is
	// rejected and creates no session.
	bad, err := mgr.Setup("mallory", malloryAddr,
		"RTP/AVP;unicast;client_port=6000-6001;destination="+victimAddr.String(), 1)
	if statusOf(t, err) != StatusBadRequest || bad != nil {
		t.Fatalf("reflected setup not rejected: session=%v err=%v", bad, err)
	}

	// Mallory cannot drive alice's session with a guessed id.
	if _, err := mgr.Play("mallory", session.ID, 2); statusOf(t, err) != StatusSessionNotFound {
		t.Fatalf("hijack PLAY: want %d, got %v", StatusSessionNotFound, err)
	}
	if err := mgr.Teardown("mallory", session.ID, 2); statusOf(t, err) != StatusSessionNotFound {
		t.Fatalf("hijack TEARDOWN: want %d, got %v", StatusSessionNotFound, err)
	}

	if _, err := mgr.Play("alice", session.ID, 2); err != nil {
		t.Fatalf("alice play: %v", err)
	}

	sink := &recordingSink{}
	wantDst := netip.AddrPortFrom(aliceAddr, 5000)
	for i := 0; i < 5; i++ {
		if err := mgr.Deliver(session.ID, sink, []byte{byte(i)}); err != nil {
			t.Fatalf("deliver %d: %v", i, err)
		}
	}

	if len(sink.dsts) != 5 {
		t.Fatalf("delivery count: want 5, got %d", len(sink.dsts))
	}
	for _, dst := range sink.dsts {
		if dst != wantDst {
			t.Fatalf("packet sent to %v, want only %v", dst, wantDst)
		}
		if dst.Addr() == victimAddr || dst.Addr() == malloryAddr {
			t.Fatalf("media leaked to non-owner address %v", dst)
		}
	}

	// A replayed/rewound CSeq from alice herself is still refused.
	if _, err := mgr.Play("alice", session.ID, 2); statusOf(t, err) != StatusBadRequest {
		t.Fatalf("CSeq rewind: want %d, got %v", StatusBadRequest, err)
	}

	if err := mgr.Teardown("alice", session.ID, 3); err != nil {
		t.Fatalf("alice teardown: %v", err)
	}
	if err := mgr.Deliver(session.ID, sink, []byte("x")); statusOf(t, err) != StatusSessionNotFound {
		t.Fatalf("delivery after teardown: want %d, got %v", StatusSessionNotFound, err)
	}
	if len(sink.dsts) != 5 {
		t.Fatalf("sink received %d destinations after teardown, want still 5", len(sink.dsts))
	}
}

func TestPortPairValidation(t *testing.T) {
	source := mustAddr(t, "203.0.113.10")
	cases := map[string]string{
		"missing pair":    "RTP/AVP;unicast",
		"single port":     "RTP/AVP;unicast;client_port=5000",
		"odd start":       "RTP/AVP;unicast;client_port=5001-5002",
		"non consecutive": "RTP/AVP;unicast;client_port=5000-5002",
		"zero port":       "RTP/AVP;unicast;client_port=0-1",
		"non numeric":     "RTP/AVP;unicast;client_port=abc-def",
		"multicast":       "RTP/AVP;multicast;client_port=5000-5001",
		"wrong profile":   "RTP/AVP/TCP;unicast;client_port=5000-5001",
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseTransport(header, source); statusOf(t, err) != StatusBadRequest {
				t.Fatalf("want %d, got %v", StatusBadRequest, err)
			}
		})
	}

	spec, err := ParseTransport(validTransport, source)
	if err != nil {
		t.Fatalf("valid header: %v", err)
	}
	if spec.ClientPort != 5000 || spec.Destination != source {
		t.Fatalf("unexpected spec: %+v", spec)
	}
}

func TestSessionIDIsRandom128Bit(t *testing.T) {
	seen := make(map[SessionID]struct{}, 64)
	for i := 0; i < 64; i++ {
		id, err := newSessionID()
		if err != nil {
			t.Fatalf("random id: %v", err)
		}
		if id == (SessionID{}) {
			t.Fatal("session id came back all zero")
		}
		if _, dup := seen[id]; dup {
			t.Fatal("duplicate 128-bit session id")
		}
		seen[id] = struct{}{}

		parsed, err := ParseSessionID(id.String())
		if err != nil || !parsed.equal(id) {
			t.Fatalf("hex round trip failed: %v", err)
		}
	}

	for _, bad := range []string{"", "abcd", "zz000000000000000000000000000000"} {
		if _, err := ParseSessionID(bad); statusOf(t, err) != StatusBadRequest {
			t.Fatalf("ParseSessionID(%q): want %d, got %v", bad, StatusBadRequest, err)
		}
	}
}

func TestSetupPinsIPv6Source(t *testing.T) {
	mgr := NewManager()
	source := mustAddr(t, "2001:db8::1")
	session, err := mgr.Setup("alice", source,
		"RTP/AVP;unicast;client_port=4000-4001;destination="+source.String(), 1)
	if err != nil {
		t.Fatalf("ipv6 setup: %v", err)
	}
	want := netip.AddrPortFrom(source, 4000)
	if got := session.Target.rtpAddrPort(); got != want {
		t.Fatalf("ipv6 target: got %v, want %v", got, want)
	}
}

func TestAddrFromNetAddr(t *testing.T) {
	tcp := &net.TCPAddr{IP: net.ParseIP("203.0.113.10"), Port: 8554}
	addr, err := AddrFromNetAddr(tcp)
	if err != nil || addr.String() != "203.0.113.10" {
		t.Fatalf("tcp addr: %v %v", addr, err)
	}

	udp := &net.UDPAddr{IP: net.ParseIP("2001:db8::1"), Port: 9000}
	addr, err = AddrFromNetAddr(udp)
	if err != nil || addr.String() != "2001:db8::1" {
		t.Fatalf("udp addr: %v %v", addr, err)
	}
}

// TestConcurrentSessions exercises the manager from many goroutines so the
// race detector can verify locking around setup, control and delivery.
func TestConcurrentSessions(t *testing.T) {
	mgr := NewManager()
	source := mustAddr(t, "203.0.113.10")
	sink := &recordingSink{}

	done := make(chan struct{})
	for worker := 0; worker < 8; worker++ {
		go func(worker int) {
			defer func() { done <- struct{}{} }()
			principal := Principal("user-" + strconv.Itoa(worker))
			for i := uint32(1); i <= 20; i++ {
				session, err := mgr.Setup(principal, source, validTransport, i)
				if err != nil {
					t.Errorf("setup: %v", err)
					return
				}
				if _, err := mgr.Play(principal, session.ID, i+100); err != nil {
					t.Errorf("play: %v", err)
					return
				}
				if err := mgr.Deliver(session.ID, sink, []byte("p")); err != nil {
					t.Errorf("deliver: %v", err)
					return
				}
				if err := mgr.Teardown(principal, session.ID, i+200); err != nil {
					t.Errorf("teardown: %v", err)
					return
				}
			}
		}(worker)
	}
	for worker := 0; worker < 8; worker++ {
		<-done
	}

	if len(sink.dsts) != 8*20 {
		t.Fatalf("deliveries: want %d, got %d", 8*20, len(sink.dsts))
	}
	want := netip.AddrPortFrom(source, 5000)
	for _, dst := range sink.dsts {
		if dst != want {
			t.Fatalf("concurrent delivery target %v, want %v", dst, want)
		}
	}
}
