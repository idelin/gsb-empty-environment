package sctpauth

import (
	"errors"
	"sync"
	"testing"
)

func TestAssociationLifecycleAndAccessors(t *testing.T) {
	server := newTestServer(t)
	assoc, _ := handshake(t, server)

	if got, err := server.Association(assoc.PeerTag()); err != nil || got != assoc {
		t.Fatalf("association lookup: %v", err)
	}
	if _, err := server.Association(424242); !errors.Is(err, ErrUnknownAssociation) {
		t.Fatalf("missing association error = %v, want ErrUnknownAssociation", err)
	}

	if assoc.LocalTag() == 0 || assoc.Window().WindowSize() != 4 {
		t.Fatal("local tag or receive window not configured")
	}

	primary, err := assoc.Paths().Primary()
	if err != nil || primary != peerPrimary {
		t.Fatalf("primary path = %q (%v)", primary, err)
	}
	if len(assoc.Paths().Snapshot()) != 2 {
		t.Fatalf("snapshot path count = %d, want 2", len(assoc.Paths().Snapshot()))
	}
	if unconfirmed := assoc.Paths().UnconfirmedAddrs(); len(unconfirmed) != 1 {
		t.Fatalf("unconfirmed count = %d, want 1", len(unconfirmed))
	}

	server.RemoveAssociation(assoc.PeerTag())
	if server.AssociationCount() != 0 {
		t.Fatalf("count after remove = %d, want 0", server.AssociationCount())
	}
}

func TestPathValidationFailureAndRejection(t *testing.T) {
	server := newTestServer(t)
	assoc, _ := handshake(t, server)

	challenge, err := assoc.StartPathValidation(peerSecondary)
	if err != nil {
		t.Fatalf("challenge: %v", err)
	}
	badNonce := challenge
	badNonce[7]++

	wrongNonce := Packet{
		SourceAddr:      peerSecondary,
		VerificationTag: assoc.PeerTag(),
		Chunks:          []Chunk{{Type: ChunkHeartbeatAck, HasNonce: true, Nonce: badNonce}},
	}
	if err := assoc.HandlePacket(wrongNonce); !errors.Is(err, ErrPathUnconfirmed) {
		t.Fatalf("wrong nonce error = %v, want ErrPathUnconfirmed", err)
	}
	fails, _ := assoc.Paths().FailCount(peerSecondary)
	if fails != 1 {
		t.Fatalf("fail count = %d, want 1", fails)
	}

	malformedHB := Packet{
		SourceAddr:      peerSecondary,
		VerificationTag: assoc.PeerTag(),
		Chunks:          []Chunk{{Type: ChunkHeartbeatAck}},
	}
	if err := assoc.HandlePacket(malformedHB); !errors.Is(err, ErrInvalidChunk) {
		t.Fatalf("heartbeat without nonce error = %v, want ErrInvalidChunk", err)
	}

	badASCONF := Packet{
		SourceAddr:      peerPrimary,
		VerificationTag: assoc.PeerTag(),
		Chunks:          []Chunk{{Type: ChunkASCONF}},
	}
	if err := assoc.HandlePacket(badASCONF); !errors.Is(err, ErrInvalidChunk) {
		t.Fatalf("asconf without address error = %v, want ErrInvalidChunk", err)
	}

	unknownChunk := Packet{
		SourceAddr:      peerPrimary,
		VerificationTag: assoc.PeerTag(),
		Chunks:          []Chunk{{Type: 250}},
	}
	if err := assoc.HandlePacket(unknownChunk); !errors.Is(err, ErrInvalidChunk) {
		t.Fatalf("unknown chunk error = %v, want ErrInvalidChunk", err)
	}
}

func TestReceiveWindowGapFillAndPending(t *testing.T) {
	window := NewReceiveWindow(10, 4)
	if err := window.Accept(12); err != nil {
		t.Fatalf("gap accept: %v", err)
	}
	if window.PendingCount() != 1 || window.CumulativeTSN() != 9 {
		t.Fatalf("pending = %d cumulative = %d", window.PendingCount(), window.CumulativeTSN())
	}
	if err := window.Accept(11); err != nil {
		t.Fatalf("fill gap: %v", err)
	}
	if err := window.Accept(10); err != nil {
		t.Fatalf("fill base: %v", err)
	}
	if window.CumulativeTSN() != 12 || window.PendingCount() != 0 {
		t.Fatalf("after contiguous fill cumulative = %d pending = %d",
			window.CumulativeTSN(), window.PendingCount())
	}
}

func TestConcurrentHandshakeAndPacketHandling(t *testing.T) {
	server := NewServer([]byte("concurrency-seed-0123456789-abcdef"))

	initAck, err := server.HandleINIT(InitRequest{
		SourceAddr:  peerPrimary,
		InitiateTag: 777,
		InitiateTSN: 300,
	})
	if err != nil {
		t.Fatalf("INIT: %v", err)
	}
	assoc, _, err := server.HandleCookieEcho(CookieEcho{
		SourceAddr:      peerPrimary,
		VerificationTag: 777,
		Cookie:          initAck.Cookie,
	})
	if err != nil {
		t.Fatalf("COOKIE-ECHO: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			server.RotateKeys([]byte("rotation-secret"))
			_, _ = server.Association(777)
			_ = assoc.Paths().IsConfirmed(peerPrimary)
		}()
	}
	wg.Wait()
}
