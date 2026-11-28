package wireguard

import (
	"bytes"
	"crypto/rand"
	"errors"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Unix(1700000000, 0)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func (c *testClock) Set(t time.Time) {
	c.mu.Lock()
	c.now = t
	c.mu.Unlock()
}

var (
	initiatorSource = netip.MustParseAddrPort("192.0.2.10:51820")
	responderSource = netip.MustParseAddrPort("192.0.2.20:51820")
)

func newTestPeers(t *testing.T, clock *testClock, limits *Limits) (*Peer, *Peer) {
	return newTestPeersPSK(t, clock, limits, [32]byte{})
}

func newTestPeersPSK(t *testing.T, clock *testClock, limits *Limits, psk [32]byte) (*Peer, *Peer) {
	t.Helper()
	aKeys, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	bKeys, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	a, err := NewPeer(Config{
		LocalPrivateKey: aKeys.Private,
		RemotePublicKey: bKeys.Public,
		PresharedKey:    psk,
		Rand:            rand.Reader,
		Now:             clock.Now,
		Limits:          limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewPeer(Config{
		LocalPrivateKey: bKeys.Private,
		RemotePublicKey: aKeys.Public,
		PresharedKey:    psk,
		Rand:            rand.Reader,
		Now:             clock.Now,
		Limits:          limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	return a, b
}

func TestPresharedKeyAndResponseAuthentication(t *testing.T) {
	clock := newTestClock()
	var psk [32]byte
	for i := range psk {
		psk[i] = byte(0xa0 + i)
	}
	initiator, responder := newTestPeersPSK(t, clock, nil, psk)
	establish(t, initiator, responder)
	packet, err := initiator.Encrypt([]byte("psk protected"))
	if err != nil {
		t.Fatal(err)
	}
	if payload, err := responder.Decrypt(packet); err != nil || string(payload) != "psk protected" {
		t.Fatalf("psk transport: payload=%q err=%v", payload, err)
	}

	clock.Advance(time.Second)
	other, err := NewPeer(Config{
		LocalPrivateKey: initiator.local.Private,
		RemotePublicKey: responder.local.Public,
		Rand:            rand.Reader,
		Now:             clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	init, err := other.Initiate()
	if err != nil {
		t.Fatal(err)
	}
	// The responder still derives a response because the initiation is not
	// PSK-bound, but the initiator must reject the response's PSK confirmation.
	result, err := responder.HandlePacket(init, initiatorSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.HandlePacket(result.Reply, responderSource); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("PSK mismatch response = %v, want ErrAuthentication", err)
	}
	if snap := other.Sessions(); snap.Current != nil {
		t.Fatalf("failed response promoted a session: %+v", snap)
	}
}

func initiateAndRespond(t *testing.T, initiator, responder *Peer) ([]byte, []byte) {
	t.Helper()
	init, err := initiator.Initiate()
	if err != nil {
		t.Fatal(err)
	}
	result, err := responder.HandlePacket(init, initiatorSource)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != ActionHandshakeResponse {
		t.Fatalf("action = %v, want handshake response", result.Action)
	}
	return init, result.Reply
}

func completeHandshake(t *testing.T, initiator, responder *Peer) {
	t.Helper()
	_, response := initiateAndRespond(t, initiator, responder)
	result, err := initiator.HandlePacket(response, responderSource)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != ActionSessionEstablished {
		t.Fatalf("action = %v, want session established", result.Action)
	}
}

func confirmResponder(t *testing.T, initiator, responder *Peer) {
	t.Helper()
	packet, err := initiator.Encrypt(nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := responder.HandlePacket(packet, initiatorSource)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != ActionTransport || !result.Confirmed {
		t.Fatalf("confirmation result = %+v", result)
	}
}

func establish(t *testing.T, initiator, responder *Peer) {
	t.Helper()
	completeHandshake(t, initiator, responder)
	confirmResponder(t, initiator, responder)
}

func TestResponderDelaysNextPromotionUntilFirstTransport(t *testing.T) {
	clock := newTestClock()
	initiator, responder := newTestPeers(t, clock, nil)

	_, response := initiateAndRespond(t, initiator, responder)
	snap := responder.Sessions()
	if snap.Current != nil || snap.Previous != nil || snap.Next == nil {
		t.Fatalf("responder slots after response = %+v", snap)
	}
	if _, err := responder.Send([]byte("must not use Next")); !errors.Is(err, ErrHandshakeUnconfirmed) {
		t.Fatalf("responder Send before confirmation = %v, want ErrHandshakeUnconfirmed", err)
	}
	if snap = responder.Sessions(); snap.Next == nil || snap.Current != nil {
		t.Fatalf("failed Send changed responder slots: %+v", snap)
	}

	if result, err := initiator.HandlePacket(response, responderSource); err != nil || result.Action != ActionSessionEstablished {
		t.Fatalf("initiator response: result=%+v err=%v", result, err)
	}
	snap = initiator.Sessions()
	if snap.Current == nil || snap.Previous != nil || snap.Next != nil {
		t.Fatalf("initiator slots after response = %+v", snap)
	}

	first, err := initiator.Encrypt(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != transportHeaderSize+poly1305TagSize {
		t.Fatalf("keepalive length = %d", len(first))
	}
	result, err := responder.HandlePacket(first, initiatorSource)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Confirmed || len(result.Payload) != 0 {
		t.Fatalf("confirmation result = %+v", result)
	}
	snap = responder.Sessions()
	if snap.Current == nil || snap.Next != nil || snap.Previous != nil {
		t.Fatalf("responder slots after confirmation = %+v", snap)
	}
	if _, err := responder.Send([]byte("now allowed")); err != nil {
		t.Fatalf("responder Send after confirmation: %v", err)
	}
}

func TestThreeSlotRotationAndPreviousReceive(t *testing.T) {
	clock := newTestClock()
	initiator, responder := newTestPeers(t, clock, nil)
	establish(t, initiator, responder)

	first := initiator.Sessions()
	responderBefore := responder.Sessions()
	oldPacket, err := initiator.Encrypt([]byte("in flight on old key"))
	if err != nil {
		t.Fatal(err)
	}

	_, response := initiateAndRespond(t, initiator, responder)
	responderDuring := responder.Sessions()
	if responderDuring.Current == nil || responderDuring.Next == nil || responderDuring.Current.LocalIndex != responderBefore.Current.LocalIndex {
		t.Fatalf("responder slots during rekey = %+v", responderDuring)
	}
	oldSend, err := responder.Send([]byte("still old current"))
	if err != nil {
		t.Fatalf("responder send with pending Next: %v", err)
	}
	if got, err := initiator.Decrypt(oldSend.Packet); err != nil || string(got) != "still old current" {
		t.Fatalf("initiator old-current receive: got=%q err=%v", got, err)
	}
	if result, err := initiator.HandlePacket(response, responderSource); err != nil || result.Action != ActionSessionEstablished {
		t.Fatalf("initiator rekey response: result=%+v err=%v", result, err)
	}
	second := initiator.Sessions()
	if second.Current == nil || second.Previous == nil || second.Current.LocalIndex == first.Current.LocalIndex {
		t.Fatalf("initiator slots after rekey = %+v", second)
	}
	if second.Previous.LocalIndex != first.Current.LocalIndex {
		t.Fatalf("initiator previous = %+v, want old current %+v", second.Previous, first.Current)
	}

	confirmResponder(t, initiator, responder)
	rotated := responder.Sessions()
	if rotated.Current == nil || rotated.Previous == nil || rotated.Next != nil {
		t.Fatalf("responder slots after confirmation = %+v", rotated)
	}
	if rotated.Current.LocalIndex != responderDuring.Next.LocalIndex ||
		rotated.Previous.LocalIndex != responderBefore.Current.LocalIndex {
		t.Fatalf("responder rotation = %+v", rotated)
	}

	result, err := responder.HandlePacket(oldPacket, initiatorSource)
	if err != nil {
		t.Fatalf("delayed packet on Previous: %v", err)
	}
	if result.Confirmed || string(result.Payload) != "in flight on old key" {
		t.Fatalf("delayed packet result = %+v", result)
	}
	if snap := responder.Sessions(); snap.Previous == nil || snap.Next != nil {
		t.Fatalf("Previous was disturbed: %+v", snap)
	}
}

func TestSimultaneousHandshakesConverge(t *testing.T) {
	clock := newTestClock()
	a, b := newTestPeers(t, clock, nil)

	initA, err := a.Initiate()
	if err != nil {
		t.Fatal(err)
	}
	initB, err := b.Initiate()
	if err != nil {
		t.Fatal(err)
	}
	respToA, err := b.HandlePacket(initA, initiatorSource)
	if err != nil || respToA.Action != ActionHandshakeResponse {
		t.Fatalf("B handles A initiation: result=%+v err=%v", respToA, err)
	}
	respToB, err := a.HandlePacket(initB, responderSource)
	if err != nil || respToB.Action != ActionHandshakeResponse {
		t.Fatalf("A handles B initiation: result=%+v err=%v", respToB, err)
	}
	if _, err := a.HandlePacket(respToA.Reply, responderSource); err != nil {
		t.Fatal(err)
	}
	if _, err := b.HandlePacket(respToB.Reply, initiatorSource); err != nil {
		t.Fatal(err)
	}
	if a.Sessions().Next == nil || b.Sessions().Next == nil {
		t.Fatalf("simultaneous responder slots lost: a=%+v b=%+v", a.Sessions(), b.Sessions())
	}

	keepaliveA, err := a.Encrypt(nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.HandlePacket(keepaliveA, initiatorSource)
	if err != nil || !result.Confirmed {
		t.Fatalf("B confirms A session: result=%+v err=%v", result, err)
	}
	keepaliveB, err := b.Encrypt(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Decrypt(keepaliveB); err != nil {
		t.Fatalf("A receives B data after convergence: %v", err)
	}
}

func TestEitherSideCanInitiateRekey(t *testing.T) {
	clock := newTestClock()
	a, b := newTestPeers(t, clock, nil)
	establish(t, a, b)
	beforeA := a.Sessions()
	beforeB := b.Sessions()

	init, err := b.Initiate()
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.HandlePacket(init, responderSource)
	if err != nil || result.Action != ActionHandshakeResponse {
		t.Fatalf("A response to B rekey: result=%+v err=%v", result, err)
	}
	if result, err := b.HandlePacket(result.Reply, initiatorSource); err != nil || result.Action != ActionSessionEstablished {
		t.Fatalf("B promotes rekey: result=%+v err=%v", result, err)
	}
	if snap := a.Sessions(); snap.Next == nil || snap.Current == nil {
		t.Fatalf("A pending rekey slots = %+v", snap)
	}
	keepalive, err := b.Encrypt(nil)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := a.HandlePacket(keepalive, responderSource); err != nil || !result.Confirmed {
		t.Fatalf("A confirms B-initiated rekey: result=%+v err=%v", result, err)
	}

	afterA, afterB := a.Sessions(), b.Sessions()
	if afterA.Current.LocalIndex == beforeA.Current.LocalIndex ||
		afterA.Previous.LocalIndex != beforeA.Current.LocalIndex ||
		afterB.Current.LocalIndex == beforeB.Current.LocalIndex ||
		afterB.Previous.LocalIndex != beforeB.Current.LocalIndex {
		t.Fatalf("bidirectional rotation failed: before A=%+v B=%+v after A=%+v B=%+v",
			beforeA, beforeB, afterA, afterB)
	}
}

func TestHandshakeTimestampReplayRejected(t *testing.T) {
	clock := newTestClock()
	initiator, responder := newTestPeers(t, clock, nil)

	init, err := initiator.Initiate()
	if err != nil {
		t.Fatal(err)
	}
	if result, err := responder.HandlePacket(init, initiatorSource); err != nil || result.Action != ActionHandshakeResponse {
		t.Fatalf("first initiation: result=%+v err=%v", result, err)
	}
	if _, err := responder.HandlePacket(init, initiatorSource); !errors.Is(err, ErrReplay) {
		t.Fatalf("replayed initiation = %v, want ErrReplay", err)
	}

	second, err := initiator.Initiate()
	if err != nil {
		t.Fatal(err)
	}
	if result, err := responder.HandlePacket(second, initiatorSource); err != nil || result.Action != ActionHandshakeResponse {
		t.Fatalf("strictly newer initiation: result=%+v err=%v", result, err)
	}
	if _, err := responder.HandlePacket(init, initiatorSource); !errors.Is(err, ErrReplay) {
		t.Fatalf("older initiation after newer one = %v, want ErrReplay", err)
	}
}

func TestTimestampGeneratorStrictlyMonotonic(t *testing.T) {
	now := time.Unix(1700000000, 500)
	var gen timestampGenerator
	first := gen.Next(now)
	second := gen.Next(now)
	third := gen.Next(now.Add(-time.Hour))
	if !(first.Compare(second) < 0 && second.Compare(third) < 0) {
		t.Fatalf("timestamps not strictly increasing: %v %v %v", first, second, third)
	}
	var checker timestampChecker
	if !checker.Accept(first) || checker.Accept(first) || !checker.Accept(third) || checker.Accept(second) {
		t.Fatal("timestamp checker accepted non-monotonic values")
	}
}

type countingSuite struct {
	Suite
	dhCalls atomic.Int64
}

func (s *countingSuite) SharedSecret(private, public [32]byte) ([32]byte, error) {
	s.dhCalls.Add(1)
	return s.Suite.SharedSecret(private, public)
}

func TestUnderLoadCookieChallengeAndMAC2Retry(t *testing.T) {
	clock := newTestClock()
	initiator, responder := newTestPeers(t, clock, nil)
	suite := &countingSuite{Suite: DefaultSuite{}}
	responder.mu.Lock()
	responder.suite = suite
	responder.mu.Unlock()
	responder.SetUnderLoad(true)

	init, err := initiator.Initiate()
	if err != nil {
		t.Fatal(err)
	}
	result, err := responder.HandlePacket(init, initiatorSource)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != ActionCookieReply || len(result.Reply) != cookieReplySize {
		t.Fatalf("first under-load result = %+v len=%d", result, len(result.Reply))
	}
	if suite.dhCalls.Load() != 0 {
		t.Fatalf("under load without MAC2 performed %d DH operations", suite.dhCalls.Load())
	}
	if snap := responder.Sessions(); snap.Next != nil || snap.Current != nil {
		t.Fatalf("cookie reply created a session: %+v", snap)
	}

	if result, err := initiator.HandlePacket(result.Reply, responderSource); err != nil || result.Action != ActionCookieStored {
		t.Fatalf("cookie stored: result=%+v err=%v", result, err)
	}
	retry, err := initiator.Initiate()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(retry[initiationMAC2Offset:initiationMAC2Offset+16], make([]byte, 16)) {
		t.Fatal("retry has empty MAC2")
	}
	result, err = responder.HandlePacket(retry, initiatorSource)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != ActionHandshakeResponse {
		t.Fatalf("MAC2 retry action = %v", result.Action)
	}
	if suite.dhCalls.Load() == 0 {
		t.Fatal("MAC2-authorized handshake did not perform DH")
	}
	if snap := responder.Sessions(); snap.Next == nil || snap.Current != nil {
		t.Fatalf("MAC2 retry slots = %+v", snap)
	}

	if result, err := initiator.HandlePacket(result.Reply, responderSource); err != nil || result.Action != ActionSessionEstablished {
		t.Fatalf("initiator response after retry: result=%+v err=%v", result, err)
	}
	confirmResponder(t, initiator, responder)
	if snap := responder.Sessions(); snap.Current == nil || snap.Next != nil {
		t.Fatalf("responder did not confirm after cookie retry: %+v", snap)
	}
}

func TestUnderLoadRejectsBadMAC1WithoutCookie(t *testing.T) {
	clock := newTestClock()
	initiator, responder := newTestPeers(t, clock, nil)
	responder.SetUnderLoad(true)
	init, err := initiator.Initiate()
	if err != nil {
		t.Fatal(err)
	}
	init[20] ^= 0xff
	if _, err := responder.HandlePacket(init, initiatorSource); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("bad MAC1 = %v, want ErrAuthentication", err)
	}
}

func TestInitiatorUnderLoadChallengesResponse(t *testing.T) {
	clock := newTestClock()
	initiator, responder := newTestPeers(t, clock, nil)
	initiator.SetUnderLoad(true)

	init, err := initiator.Initiate()
	if err != nil {
		t.Fatal(err)
	}
	result, err := responder.HandlePacket(init, initiatorSource)
	if err != nil || result.Action != ActionHandshakeResponse {
		t.Fatalf("responder initiation: result=%+v err=%v", result, err)
	}
	firstResponse := result.Reply
	result, err = initiator.HandlePacket(firstResponse, responderSource)
	if err != nil || result.Action != ActionCookieReply || len(result.Reply) != cookieReplySize {
		t.Fatalf("initiator under load: result=%+v err=%v", result, err)
	}
	if snap := initiator.Sessions(); snap.Current != nil {
		t.Fatalf("challenged response established a session: %+v", snap)
	}

	result, err = responder.HandlePacket(result.Reply, initiatorSource)
	if err != nil || result.Action != ActionHandshakeResponse {
		t.Fatalf("responder cookie reply: result=%+v err=%v", result, err)
	}
	resend := result.Reply
	if bytes.Equal(resend[responseMAC2Offset:responseMAC2Offset+16], make([]byte, 16)) {
		t.Fatal("resent response has empty MAC2")
	}
	result, err = initiator.HandlePacket(resend, responderSource)
	if err != nil || result.Action != ActionSessionEstablished {
		t.Fatalf("initiator accepted MAC2 response: result=%+v err=%v", result, err)
	}
	confirmResponder(t, initiator, responder)
}

func TestCookieSecretRotates(t *testing.T) {
	clock := newTestClock()
	initiator, responder := newTestPeers(t, clock, nil)
	responder.SetUnderLoad(true)

	challenge := func() []byte {
		init, err := initiator.Initiate()
		if err != nil {
			t.Fatal(err)
		}
		result, err := responder.HandlePacket(init, initiatorSource)
		if err != nil {
			t.Fatal(err)
		}
		if result.Action != ActionCookieReply {
			t.Fatalf("expected cookie reply, got %v", result.Action)
		}
		return result.Reply
	}
	firstCookie := challenge()
	if result, err := initiator.HandlePacket(firstCookie, responderSource); err != nil || result.Action != ActionCookieStored {
		t.Fatalf("store first cookie: result=%+v err=%v", result, err)
	}

	clock.Advance(CookieRefreshTime + time.Second)
	if result, err := responder.HandlePacket(mustInitiate(t, initiator), initiatorSource); err != nil {
		t.Fatal(err)
	} else if result.Action != ActionHandshakeResponse {
		t.Fatalf("old cookie should still work via previous secret, got %v", result.Action)
	}

	clock.Advance(CookieRefreshTime + time.Second)
	result, err := responder.HandlePacket(mustInitiate(t, initiator), initiatorSource)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != ActionCookieReply {
		t.Fatalf("two-period-old cookie action = %v, want cookie reply", result.Action)
	}
}

func mustInitiate(t *testing.T, p *Peer) []byte {
	t.Helper()
	pkt, err := p.Initiate()
	if err != nil {
		t.Fatal(err)
	}
	return pkt
}

func TestRekeyAndRejectMessageLimits(t *testing.T) {
	clock := newTestClock()
	limits := DefaultLimits()
	limits.RekeyAfterMessages = 5
	limits.RejectAfterMessages = 8
	initiator, responder := newTestPeers(t, clock, &limits)
	establish(t, initiator, responder)

	for i := 0; i < 3; i++ {
		result, err := initiator.Send([]byte{byte(i)})
		if err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		if result.RekeyNeeded {
			t.Fatalf("rekey flagged too early at send %d", i)
		}
		if _, err := responder.Decrypt(result.Packet); err != nil {
			t.Fatalf("decrypt %d: %v", i, err)
		}
	}
	if initiator.NeedsRekey() {
		t.Fatal("NeedsRekey true before threshold")
	}
	result, err := initiator.Send([]byte{3})
	if err != nil {
		t.Fatal(err)
	}
	if !result.RekeyNeeded {
		t.Fatal("rekey not flagged on threshold-crossing send")
	}
	if _, err := responder.Decrypt(result.Packet); err != nil {
		t.Fatalf("old key interrupted at rekey threshold: %v", err)
	}
	if !initiator.NeedsRekey() {
		t.Fatal("NeedsRekey false after threshold")
	}

	for i := 0; i < 3; i++ {
		if _, err := initiator.Send([]byte{byte(4 + i)}); err != nil {
			t.Fatalf("send after rekey threshold %d: %v", i, err)
		}
	}
	if _, err := initiator.Send([]byte{7}); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("send past reject limit = %v, want ErrSessionExpired", err)
	}
	if snap := initiator.Sessions(); snap.Current != nil {
		t.Fatalf("rejected session still present: %+v", snap)
	}
}

func TestRekeyAfterTimeAndHardDestroyAfter180s(t *testing.T) {
	clock := newTestClock()
	initiator, responder := newTestPeers(t, clock, nil)
	establish(t, initiator, responder)

	clock.Advance(RekeyAfterTime - time.Second)
	if initiator.NeedsRekey() {
		t.Fatal("rekey early")
	}
	packet, err := initiator.Encrypt([]byte("before rekey"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := responder.Decrypt(packet); err != nil {
		t.Fatal(err)
	}

	clock.Advance(time.Second)
	if !initiator.NeedsRekey() || !responder.NeedsRekey() {
		t.Fatal("rekey not requested at 120s")
	}
	packet, err = initiator.Encrypt([]byte("old key still works"))
	if err != nil {
		t.Fatalf("old key interrupted at soft rekey: %v", err)
	}
	if _, err := responder.Decrypt(packet); err != nil {
		t.Fatalf("old key decrypt at soft rekey: %v", err)
	}

	clock.Advance(RejectAfterTime - RekeyAfterTime - time.Second)
	delayed, err := initiator.Encrypt([]byte("must not decrypt later"))
	if err != nil {
		t.Fatalf("send at 179s: %v", err)
	}
	clock.Advance(time.Second)
	if _, err := initiator.Send([]byte("expired")); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("send at 180s = %v, want ErrSessionExpired", err)
	}
	if snap := initiator.Sessions(); snap.Current != nil {
		t.Fatalf("expired send kept current: %+v", snap)
	}
	if _, err := responder.Decrypt(delayed); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("decrypt at 180s = %v, want ErrSessionExpired", err)
	}
	if snap := responder.Sessions(); snap.Current != nil {
		t.Fatalf("expired receive kept current: %+v", snap)
	}
}

func TestExpireErasesUnconfirmedNext(t *testing.T) {
	clock := newTestClock()
	initiator, responder := newTestPeers(t, clock, nil)
	if _, response := initiateAndRespond(t, initiator, responder); len(response) == 0 {
		t.Fatal("empty response")
	}
	if responder.Sessions().Next == nil {
		t.Fatal("no Next session to expire")
	}
	expiredIndex := responder.Sessions().Next.LocalIndex
	clock.Advance(RejectAfterTime)
	responder.Expire()
	snap := responder.Sessions()
	if snap.Next != nil || snap.Current != nil || snap.Previous != nil {
		t.Fatalf("Expire left sessions: %+v", snap)
	}
	// The erased index cannot be used for transport anymore.
	orphan := marshalTransport(expiredIndex, 0, make([]byte, poly1305TagSize))
	if _, err := responder.Decrypt(orphan); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("orphan transport = %v, want ErrUnknownSession", err)
	}
}

func TestSnapshotEnforcesHardExpiry(t *testing.T) {
	clock := newTestClock()
	initiator, responder := newTestPeers(t, clock, nil)
	establish(t, initiator, responder)

	clock.Advance(RejectAfterTime)
	if snap := initiator.Sessions(); snap.Current != nil || snap.Previous != nil || snap.Next != nil {
		t.Fatalf("hard-expired snapshot = %+v", snap)
	}
	if _, err := initiator.Send([]byte("expired")); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("send after expired snapshot = %v, want ErrSessionExpired", err)
	}
	if _, err := initiator.Send([]byte("again")); !errors.Is(err, ErrNoSession) {
		t.Fatalf("second send after expiry = %v, want ErrNoSession", err)
	}
}

func TestTransportReplayWindowAndTamper(t *testing.T) {
	clock := newTestClock()
	initiator, responder := newTestPeers(t, clock, nil)
	establish(t, initiator, responder)

	packet, err := initiator.Encrypt([]byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := responder.Decrypt(packet); err != nil {
		t.Fatal(err)
	}
	if _, err := responder.Decrypt(packet); !errors.Is(err, ErrReplayWindow) {
		t.Fatalf("duplicate transport = %v, want ErrReplayWindow", err)
	}

	fresh, err := initiator.Encrypt([]byte("two"))
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), fresh...)
	tampered[len(tampered)-1] ^= 1
	if _, err := responder.Decrypt(tampered); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("tampered transport = %v, want ErrAuthentication", err)
	}
	if _, err := responder.Decrypt(fresh); err != nil {
		t.Fatalf("valid packet after failed tamper: %v", err)
	}
}

func TestReplayWindowBitmap(t *testing.T) {
	var w ReplayWindow
	if !w.Update(100) {
		t.Fatal("initial packet rejected")
	}
	if !w.Update(98) {
		t.Fatal("out-of-order packet rejected")
	}
	if w.Update(98) {
		t.Fatal("duplicate accepted")
	}
	if !w.Update(100 + ReplayWindowSize - 1) {
		t.Fatal("edge-of-window packet rejected")
	}
	if w.Update(1) {
		t.Fatal("packet older than window accepted")
	}
	if !w.Update(100 + ReplayWindowSize + 500) {
		t.Fatal("far-ahead packet rejected")
	}
	if w.Update(100 + ReplayWindowSize - 1) {
		t.Fatal("packet now outside shifted window accepted")
	}
}
