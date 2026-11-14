package netsec

import (
	"errors"
	"testing"
	"time"
)

func testGateway(t *testing.T, clock Clock, gen CIDGenerator) *Gateway {
	t.Helper()
	g, err := NewGateway(GatewayConfig{
		Clock:             clock,
		Generator:         gen,
		MaxPacketLifetime: 10 * time.Second,
		ConfirmationGrace: 2 * time.Second,
		MasterSecret:      testMaster,
		RecoveryKey:       testRecovery,
	})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestOldTokenReplayClosesOnlyOldConnection(t *testing.T) {
	clock := NewManualClock(time.Unix(1000, 0).UTC())
	g := testGateway(t, clock, SequentialCIDGenerator([]byte{0x51}, 8))
	a, err := g.OpenNamedConnection("conn-a", "edge-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := g.OpenNamedConnection("conn-b", "edge-a")
	if err != nil {
		t.Fatal(err)
	}
	ga, err := a.Manager.IssueCID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Manager.IssueCID(); err != nil {
		t.Fatal(err)
	}
	packetA := BuildResetPacket([]byte{0x40, 0x0a, 0x0b, 0x0c, 0x0d}, ga.Token)
	if _, err := a.Manager.Close("old connection done"); err != nil {
		t.Fatal(err)
	}

	d := g.DetectReset(packetA, ResetOptions{Route: "edge-a", Probe: func([]byte) bool { return false }})
	if !d.Matched || d.ConnectionID != "conn-a" || d.Reason != ResetAlreadyClosed {
		t.Fatalf("old token decision = %+v", d)
	}
	if b.Manager.IsClosed() {
		t.Fatal("old token closed an unrelated new connection")
	}
	if d.Closed {
		t.Fatal("replaying an old closed connection's token reported a new close")
	}

	clock.Advance(13 * time.Second)
	if _, err := a.Manager.PurgeExpired(); err != nil {
		t.Fatal(err)
	}
	d = g.DetectReset(packetA, ResetOptions{Route: "edge-a", Probe: func([]byte) bool { return false }})
	if d.Matched || d.Reason != ResetNoToken {
		t.Fatalf("expired old token decision = %+v", d)
	}
	if b.Manager.IsClosed() {
		t.Fatal("expired old token closed an unrelated connection")
	}
}

func TestCrossRoutePartitionTokenIsRejected(t *testing.T) {
	clock := NewManualClock(time.Unix(1100, 0).UTC())
	g := testGateway(t, clock, SequentialCIDGenerator([]byte{0x61}, 8))
	a, _ := g.OpenNamedConnection("conn-a", "edge-a")
	b, _ := g.OpenNamedConnection("conn-b", "edge-b")
	ga, err := a.Manager.IssueCID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Manager.IssueCID(); err != nil {
		t.Fatal(err)
	}
	packet := BuildResetPacket([]byte{0x40, 0xaa, 0xbb, 0xcc, 0xdd}, ga.Token)
	d := g.DetectReset(packet, ResetOptions{Route: "edge-b", Probe: func([]byte) bool { return false }})
	if d.Matched || d.Closed || d.Reason != ResetNoToken || d.Candidates != 0 {
		t.Fatalf("cross-partition decision = %+v", d)
	}
	if a.Manager.IsClosed() || b.Manager.IsClosed() {
		t.Fatal("cross-partition reset closed a connection")
	}

	d = g.DetectReset(packet, ResetOptions{Route: "edge-a", Probe: func([]byte) bool { return false }})
	if !d.Matched || d.ConnectionID != "conn-a" || !d.Closed {
		t.Fatalf("same-partition decision = %+v", d)
	}
	if b.Manager.IsClosed() {
		t.Fatal("reset for conn-a closed conn-b")
	}
}

func TestCrossConnectionTokenCollisionIsAmbiguous(t *testing.T) {
	clock := NewManualClock(time.Unix(1200, 0).UTC())
	g := testGateway(t, clock, SequentialCIDGenerator([]byte{0x71}, 8))
	a, _ := g.OpenNamedConnection("conn-a", "edge-a")
	b, _ := g.OpenNamedConnection("conn-b", "edge-a")
	ga, err := a.Manager.IssueCID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Manager.IssueCID(); err != nil {
		t.Fatal(err)
	}
	// Inject a live collision into conn-b. The gateway must refuse to pick an
	// owner when more than one connection matches the trailing token.
	fakeCID := []byte{0x71, 0, 0, 0, 0, 0, 0, 99}
	fake := &cidRecord{
		cid:        fakeCID,
		key:        string(fakeCID),
		sequence:   99,
		generation: ga.Generation,
		route:      "edge-a",
		token:      ga.Token,
		state:      CIDActive,
		issuedAt:   clock.Now(),
	}
	b.Manager.records[fake.key] = fake
	b.Manager.bySeq[fake.sequence] = fake
	b.Manager.tokens[fake.token] = append(b.Manager.tokens[fake.token], fake)

	packet := BuildResetPacket([]byte{0x40, 1, 2, 3, 4}, ga.Token)
	d := g.DetectReset(packet, ResetOptions{Route: "edge-a", Probe: func([]byte) bool { return false }})
	if d.Reason != ResetAmbiguous || d.Matched || d.Closed || d.Candidates != 2 {
		t.Fatalf("collision decision = %+v", d)
	}
	if d.Witness == nil || d.Witness.Kind != AuditResetAmbiguous {
		t.Fatalf("collision witness = %+v", d.Witness)
	}
	if a.Manager.IsClosed() || b.Manager.IsClosed() {
		t.Fatal("ambiguous token collision closed a connection")
	}
}

func TestGatewayRefusesCIDReissueAcrossConnections(t *testing.T) {
	clock := NewManualClock(time.Unix(1300, 0).UTC())
	fixed := CIDGeneratorFunc(func() ([]byte, error) {
		return []byte{0x81, 0, 0, 0, 0, 0, 0, 1}, nil
	})
	g := testGateway(t, clock, fixed)
	a, _ := g.OpenNamedConnection("conn-a", "edge-a")
	b, _ := g.OpenNamedConnection("conn-b", "edge-a")
	if _, err := a.Manager.IssueCID(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Manager.IssueCID(); !errors.Is(err, ErrCIDReused) {
		t.Fatalf("cross-connection CID reuse error = %v", err)
	}
}
