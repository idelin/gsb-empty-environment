package quicguard_test

import (
	"errors"
	"testing"

	"formal.local/w27-86968a8181"
)

func TestPathValidationResetsBudgetAndValidates(t *testing.T) {
	g, _ := newTestGuard(t)
	oldPath := mustAddr(t, "192.0.2.10:8000")
	newPath := mustAddr(t, "192.0.2.20:9001") // migrated source address

	// Prime and validate the old path so it has accumulated history.
	if _, err := g.OnDatagram(oldPath, handshakePacket(clientDCID, serverSCID, 500)); err != nil {
		t.Fatal(err)
	}
	g.ConfirmHandshake(oldPath)

	challenge, frame, err := g.BeginPathValidation(newPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(frame) != 9 || frame[0] != 0x1a {
		t.Fatalf("malformed PATH_CHALLENGE frame: %x", frame)
	}

	// The new path starts with a fully reset 3x budget.
	snap := g.Snapshot(newPath)
	if snap.RXBytes != 0 || snap.TXBytes != 0 || snap.Validated || !snap.PendingChallenge {
		t.Fatalf("new path not reset: %+v", snap)
	}

	// Until the response arrives, anti-amplification is enforced from zero:
	// any egress is suspended.
	if _, err := g.Send(newPath, []byte{1}); !errors.Is(err, quicguard.ErrAmplificationLimited) {
		t.Fatalf("unvalidated new path allowed egress: %v", err)
	}

	// A wrong PATH_RESPONSE is rejected in constant time and keeps the path closed.
	wrong := make([]byte, 9)
	wrong[0] = 0x1b
	if _, err := g.HandlePathResponse(newPath, wrong); !errors.Is(err, quicguard.ErrPathChallengeMismatch) {
		t.Fatalf("wrong challenge accepted: %v", err)
	}

	// Feed the challenge through the echo helper and validate the real response.
	echo, err := quicguard.HandlePathChallenge(frame)
	if err != nil {
		t.Fatal(err)
	}
	drained, err := g.HandlePathResponse(newPath, echo)
	if err != nil {
		t.Fatalf("valid PATH_RESPONSE rejected: %v", err)
	}
	if len(drained) != 1 || len(drained[0].Data) != 1 {
		t.Fatalf("suspended datagram not drained: %+v", drained)
	}
	snap = g.Snapshot(newPath)
	if !snap.Validated || snap.PendingChallenge {
		t.Fatalf("path not validated: %+v", snap)
	}

	// A response without a pending challenge is rejected.
	if _, err := g.HandlePathResponse(newPath, echo); !errors.Is(err, quicguard.ErrNoPendingChallenge) {
		t.Fatalf("replayed response accepted: %v", err)
	}

	_ = challenge
}

func TestPathFrameParsing(t *testing.T) {
	if _, _, err := quicguard.ParsePathFrame([]byte{0x1a}); !errors.Is(err, quicguard.ErrPathFrame) {
		t.Fatalf("short frame accepted: %v", err)
	}
	if _, _, err := quicguard.ParsePathFrame([]byte{0x00, 1, 2, 3, 4, 5, 6, 7, 8}); !errors.Is(err, quicguard.ErrPathFrame) {
		t.Fatalf("wrong frame type accepted: %v", err)
	}
}
