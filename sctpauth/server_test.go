package sctpauth

import (
	"errors"
	"testing"
	"time"
)

const (
	peerPrimary   = "192.0.2.10"
	peerSecondary = "203.0.113.77"
	victimIP      = "198.51.100.66"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	now, clock := newTestClock()
	server := NewServer(
		[]byte("deterministic-server-seed-entropy-0123456789"),
		WithCookieLifespan(30*time.Second),
		WithReceiveWindow(4),
		withClock(clock),
	)
	t.Cleanup(func() { _ = now })
	return server
}

func handshake(t *testing.T, server *Server) (*Association, *InitAckResponse) {
	t.Helper()
	initAck, err := server.HandleINIT(InitRequest{
		SourceAddr:     peerPrimary,
		InitiateTag:    5001,
		InitiateTSN:    9000,
		AnnouncedAddrs: []string{peerSecondary},
	})
	if err != nil {
		t.Fatalf("handle INIT: %v", err)
	}

	assoc, result, err := server.HandleCookieEcho(CookieEcho{
		SourceAddr:      peerPrimary,
		VerificationTag: 5001,
		Cookie:          initAck.Cookie,
	})
	if err != nil {
		t.Fatalf("handle COOKIE-ECHO: %v", err)
	}
	if result.LocalTag != initAck.MyTag || result.PeerTag != 5001 {
		t.Fatalf("established tags wrong: %+v", result)
	}
	if !assoc.Paths().IsConfirmed(peerPrimary) {
		t.Fatal("handshake source path must be confirmed")
	}
	return assoc, initAck
}

func TestServerFourWayHandshakeAndReplay(t *testing.T) {
	server := newTestServer(t)
	assoc, initAck := handshake(t, server)

	if server.AssociationCount() != 1 {
		t.Fatalf("association count = %d, want 1", server.AssociationCount())
	}

	_, _, err := server.HandleCookieEcho(CookieEcho{
		SourceAddr:      peerPrimary,
		VerificationTag: 5001,
		Cookie:          initAck.Cookie,
	})
	if !errors.Is(err, ErrCookieReplay) {
		t.Fatalf("cookie replay error = %v, want ErrCookieReplay", err)
	}

	if got := assoc.PeerInitialTSN(); got != 9000 {
		t.Fatalf("peer initial TSN = %d, want 9000", got)
	}
}

func TestServerRejectsForgedAndCrossSourceCookie(t *testing.T) {
	server := newTestServer(t)
	initAck, err := server.HandleINIT(InitRequest{
		SourceAddr:  peerPrimary,
		InitiateTag: 5001,
		InitiateTSN: 9000,
	})
	if err != nil {
		t.Fatalf("INIT: %v", err)
	}

	forged := append([]byte(nil), initAck.Cookie...)
	forged[len(cookieMagic)+2] ^= 0x0F
	if _, _, err := server.HandleCookieEcho(CookieEcho{
		SourceAddr:      peerPrimary,
		VerificationTag: 5001,
		Cookie:          forged,
	}); !errors.Is(err, ErrCookieSignature) {
		t.Fatalf("forged cookie error = %v, want ErrCookieSignature", err)
	}

	if _, _, err := server.HandleCookieEcho(CookieEcho{
		SourceAddr:      victimIP,
		VerificationTag: 5001,
		Cookie:          initAck.Cookie,
	}); !errors.Is(err, ErrSourceAddress) {
		t.Fatalf("third-party source error = %v, want ErrSourceAddress", err)
	}
	if server.AssociationCount() != 0 {
		t.Fatal("failed handshake must not create an association")
	}
}

func TestVerificationTagEnforcement(t *testing.T) {
	server := newTestServer(t)
	assoc, _ := handshake(t, server)

	validPacket := func() Packet {
		return Packet{
			SourceAddr:      peerPrimary,
			VerificationTag: assoc.PeerTag(),
			Chunks:          []Chunk{{Type: ChunkData, TSN: 9000}},
		}
	}

	if err := server.HandleEstablishedPacket(validPacket()); err != nil {
		t.Fatalf("valid DATA: %v", err)
	}

	wrongTag := validPacket()
	wrongTag.VerificationTag = assoc.PeerTag() + 1
	if err := assoc.HandlePacket(wrongTag); !errors.Is(err, ErrVerificationTag) {
		t.Fatalf("spoofed tag error = %v, want ErrVerificationTag", err)
	}

	zeroTag := validPacket()
	zeroTag.VerificationTag = 0
	if err := assoc.HandlePacket(zeroTag); !errors.Is(err, ErrForbiddenTag) {
		t.Fatalf("zero tag error = %v, want ErrForbiddenTag", err)
	}

	if err := server.HandleEstablishedPacket(Packet{VerificationTag: 777}); !errors.Is(err, ErrUnknownAssociation) {
		t.Fatalf("unknown association error = %v, want ErrUnknownAssociation", err)
	}
}

func TestTSNBlindInjectionAndReplayRejected(t *testing.T) {
	server := newTestServer(t)
	assoc, _ := handshake(t, server)

	packet := func(tsn uint32) Packet {
		return Packet{
			SourceAddr:      peerPrimary,
			VerificationTag: assoc.PeerTag(),
			Chunks:          []Chunk{{Type: ChunkData, TSN: tsn}},
		}
	}

	if err := server.HandleEstablishedPacket(packet(9000)); err != nil {
		t.Fatalf("first DATA: %v", err)
	}
	if err := server.HandleEstablishedPacket(packet(9000)); !errors.Is(err, ErrDuplicateTSN) {
		t.Fatalf("replayed DATA error = %v, want ErrDuplicateTSN", err)
	}
	if err := server.HandleEstablishedPacket(packet(9100)); !errors.Is(err, ErrTSNOutsideWindow) {
		t.Fatalf("off-path blind injection error = %v, want ErrTSNOutsideWindow", err)
	}
	if err := server.HandleEstablishedPacket(packet(8999)); !errors.Is(err, ErrDuplicateTSN) {
		t.Fatalf("stale TSN error = %v, want ErrDuplicateTSN", err)
	}

	mixed := Packet{
		SourceAddr:      peerPrimary,
		VerificationTag: assoc.PeerTag(),
		Chunks: []Chunk{
			{Type: ChunkData, TSN: 9001},
			{Type: ChunkData, TSN: 9999},
		},
	}
	if err := server.HandleEstablishedPacket(mixed); !errors.Is(err, ErrTSNOutsideWindow) {
		t.Fatalf("mixed packet error = %v, want ErrTSNOutsideWindow", err)
	}
	if err := assoc.Window().Check(9001); err != nil {
		t.Fatalf("rejected mixed packet must not mutate window: %v", err)
	}
}

func TestMultihomingReflectionBlockedThenValidated(t *testing.T) {
	server := newTestServer(t)
	assoc, _ := handshake(t, server)

	if err := assoc.SendData(peerSecondary, 1); !errors.Is(err, ErrPathUnconfirmed) {
		t.Fatalf("send to unconfirmed secondary error = %v, want ErrPathUnconfirmed", err)
	}

	challenge, err := assoc.StartPathValidation(peerSecondary)
	if err != nil {
		t.Fatalf("start validation: %v", err)
	}

	wrongSource := Packet{
		SourceAddr:      victimIP,
		VerificationTag: assoc.PeerTag(),
		Chunks:          []Chunk{{Type: ChunkHeartbeatAck, HasNonce: true, Nonce: challenge}},
	}
	if err := server.HandleEstablishedPacket(wrongSource); !errors.Is(err, ErrUnknownPath) {
		t.Fatalf("ack from victim IP error = %v, want ErrUnknownPath", err)
	}

	ack := Packet{
		SourceAddr:      peerSecondary,
		VerificationTag: assoc.PeerTag(),
		Chunks:          []Chunk{{Type: ChunkHeartbeatAck, HasNonce: true, Nonce: challenge}},
	}
	if err := server.HandleEstablishedPacket(ack); err != nil {
		t.Fatalf("valid heartbeat ack: %v", err)
	}
	if err := assoc.SendData(peerSecondary, 2); err != nil {
		t.Fatalf("confirmed secondary should carry data: %v", err)
	}
}

func TestASCONFAddIPIsGated(t *testing.T) {
	server := newTestServer(t)
	assoc, _ := handshake(t, server)

	asconf := Packet{
		SourceAddr:      peerPrimary,
		VerificationTag: assoc.PeerTag(),
		Chunks:          []Chunk{{Type: ChunkASCONF, HasAddIP: true, AddAddress: victimIP}},
	}
	if err := server.HandleEstablishedPacket(asconf); err != nil {
		t.Fatalf("ASCONF add IP: %v", err)
	}
	if err := assoc.SendData(victimIP, 1); !errors.Is(err, ErrPathUnconfirmed) {
		t.Fatalf("ASCONF reflection error = %v, want ErrPathUnconfirmed", err)
	}

	challenge, err := assoc.StartPathValidation(victimIP)
	if err != nil {
		t.Fatalf("challenge ASCONF address: %v", err)
	}
	ack := Packet{
		SourceAddr:      victimIP,
		VerificationTag: assoc.PeerTag(),
		Chunks:          []Chunk{{Type: ChunkHeartbeatAck, HasNonce: true, Nonce: challenge}},
	}
	if err := server.HandleEstablishedPacket(ack); err != nil {
		t.Fatalf("validate ASCONF address: %v", err)
	}
	if err := assoc.SendData(victimIP, 2); err != nil {
		t.Fatalf("validated ASCONF address: %v", err)
	}
}

func TestExpiredCookieRejectedEndToEnd(t *testing.T) {
	nowPtr, clock := newTestClock()
	server := NewServer(
		[]byte("another-deterministic-seed-abcdefghijklmnop"),
		WithCookieLifespan(5*time.Second),
		withClock(clock),
	)

	initAck, err := server.HandleINIT(InitRequest{
		SourceAddr:  peerPrimary,
		InitiateTag: 4242,
		InitiateTSN: 10,
	})
	if err != nil {
		t.Fatalf("INIT: %v", err)
	}

	*nowPtr = nowPtr.Add(6 * time.Second)
	_, _, err = server.HandleCookieEcho(CookieEcho{
		SourceAddr:      peerPrimary,
		VerificationTag: 4242,
		Cookie:          initAck.Cookie,
	})
	if !errors.Is(err, ErrCookieExpired) {
		t.Fatalf("expired cookie error = %v, want ErrCookieExpired", err)
	}
}
