package netsec

import (
	"errors"
	"testing"
	"time"
)

var testStart = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

func newTestState(t *testing.T, cfg Config) (*State, *ManualClock) {
	t.Helper()
	clock := NewManualClock(testStart)
	cfg.Clock = clock
	if cfg.LinkMTU == 0 {
		cfg.LinkMTU = 1500
	}
	if cfg.SentTTL == 0 {
		cfg.SentTTL = 10 * time.Second
	}
	if cfg.ProbeTimeout == 0 {
		cfg.ProbeTimeout = 3 * time.Second
	}
	s, err := NewState(cfg)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	return s, clock
}

// sendPacket records a packet and returns a notification quoting its
// full digest with the given reported wire MTU.
func sendPacket(t *testing.T, s *State, payload []byte, reportedMTU int) Notification {
	t.Helper()
	if _, err := s.OnSend(payload); err != nil {
		t.Fatalf("OnSend: %v", err)
	}
	d := DigestOf(payload)
	return Notification{
		PathInstance: s.PathInstance(),
		Digest:       d[:],
		ReportedMTU:  reportedMTU,
	}
}

func TestNotificationDecreasesMTU(t *testing.T) {
	s, _ := newTestState(t, Config{})
	if got := s.CurrentMTU(); got != 1500 {
		t.Fatalf("initial MTU = %d, want 1500", got)
	}
	n := sendPacket(t, s, make([]byte, 1500), 1400)
	ok, err := s.HandleNotification(n)
	if err != nil || !ok {
		t.Fatalf("HandleNotification = (%v, %v), want (true, nil)", ok, err)
	}
	if got := s.CurrentMTU(); got != 1400 {
		t.Fatalf("MTU = %d, want 1400", got)
	}
}

func TestRejectsStalePathNotification(t *testing.T) {
	s, _ := newTestState(t, Config{})
	n := sendPacket(t, s, make([]byte, 1500), 1400)
	oldInstance := s.PathInstance()
	s.ResetPath()
	if s.PathInstance() == oldInstance {
		t.Fatal("path instance did not change on reset")
	}
	if _, err := s.HandleNotification(n); !errors.Is(err, ErrStalePath) {
		t.Fatalf("err = %v, want ErrStalePath", err)
	}
	// A notification minted on the new path but replayed after another
	// reset is stale too.
	n2 := sendPacket(t, s, make([]byte, 1500), 1400)
	s.ResetPath()
	if _, err := s.HandleNotification(n2); !errors.Is(err, ErrStalePath) {
		t.Fatalf("err = %v, want ErrStalePath", err)
	}
	if got := s.CurrentMTU(); got != 1500 {
		t.Fatalf("MTU = %d, want 1500 after reset", got)
	}
}

func TestRejectsTruncatedReference(t *testing.T) {
	s, _ := newTestState(t, Config{})
	n := sendPacket(t, s, make([]byte, 1500), 1400)
	for _, cut := range []int{0, 4, 8, 16, DigestSize - 1, DigestSize + 8} {
		bad := n
		d := make([]byte, cut)
		copy(d, n.Digest)
		bad.Digest = d
		if _, err := s.HandleNotification(bad); !errors.Is(err, ErrTruncatedReference) {
			t.Fatalf("cut=%d: err = %v, want ErrTruncatedReference", cut, err)
		}
	}
	if got := s.CurrentMTU(); got != 1500 {
		t.Fatalf("MTU = %d, want 1500 (truncated refs must not apply)", got)
	}
}

func TestRejectsUnknownDigest(t *testing.T) {
	s, _ := newTestState(t, Config{})
	d := DigestOf([]byte("never sent"))
	n := Notification{PathInstance: s.PathInstance(), Digest: d[:], ReportedMTU: 1400}
	if _, err := s.HandleNotification(n); !errors.Is(err, ErrUnknownDigest) {
		t.Fatalf("err = %v, want ErrUnknownDigest", err)
	}
}

func TestRejectsExpiredDigestReference(t *testing.T) {
	s, clock := newTestState(t, Config{})
	n := sendPacket(t, s, make([]byte, 1500), 1400)
	clock.Advance(11 * time.Second) // past the 10s SentTTL
	if _, err := s.HandleNotification(n); !errors.Is(err, ErrUnknownDigest) {
		t.Fatalf("err = %v, want ErrUnknownDigest for expired entry", err)
	}
	if got := s.CurrentMTU(); got != 1500 {
		t.Fatalf("MTU = %d, want 1500", got)
	}
}

func TestRejectsInconsistentReport(t *testing.T) {
	s, _ := newTestState(t, Config{})
	// Packet wire size is 1400; a PTB must report an MTU below that.
	n := sendPacket(t, s, make([]byte, 1400), 1400)
	if _, err := s.HandleNotification(n); !errors.Is(err, ErrInconsistentReport) {
		t.Fatalf("err = %v, want ErrInconsistentReport", err)
	}
}

func TestNotificationNeverRaises(t *testing.T) {
	s, _ := newTestState(t, Config{})
	n := sendPacket(t, s, make([]byte, 1500), 1400)
	if _, err := s.HandleNotification(n); err != nil {
		t.Fatalf("HandleNotification: %v", err)
	}
	if got := s.CurrentMTU(); got != 1400 {
		t.Fatalf("MTU = %d, want 1400", got)
	}
	// A later notification reporting a larger MTU must not raise it.
	// It references the same cached 1500-byte packet, sent before the
	// decrease.
	n2 := n
	n2.ReportedMTU = 1450
	ok, err := s.HandleNotification(n2)
	if err != nil {
		t.Fatalf("HandleNotification: %v", err)
	}
	if ok {
		t.Fatal("notification raised the MTU")
	}
	if got := s.CurrentMTU(); got != 1400 {
		t.Fatalf("MTU = %d, want 1400", got)
	}
}

func TestProbeRaisesMTU(t *testing.T) {
	s, _ := newTestState(t, Config{})
	n := sendPacket(t, s, make([]byte, 1500), 1400)
	if _, err := s.HandleNotification(n); err != nil {
		t.Fatalf("HandleNotification: %v", err)
	}
	tok, err := s.BeginProbe(1450)
	if err != nil {
		t.Fatalf("BeginProbe: %v", err)
	}
	ok, err := s.CompleteProbe(tok)
	if err != nil || !ok {
		t.Fatalf("CompleteProbe = (%v, %v), want (true, nil)", ok, err)
	}
	if got := s.CurrentMTU(); got != 1450 {
		t.Fatalf("MTU = %d, want 1450", got)
	}
}

func TestProbeRaceSupersededByNewerProbe(t *testing.T) {
	s, _ := newTestState(t, Config{})
	n := sendPacket(t, s, make([]byte, 1500), 1300)
	if _, err := s.HandleNotification(n); err != nil {
		t.Fatalf("HandleNotification: %v", err)
	}
	first, err := s.BeginProbe(1500)
	if err != nil {
		t.Fatalf("BeginProbe: %v", err)
	}
	second, err := s.BeginProbe(1400)
	if err != nil {
		t.Fatalf("BeginProbe: %v", err)
	}
	// The older probe's acknowledgement arrives late and must be
	// rejected: it would otherwise restore an unverified 1500.
	if _, err := s.CompleteProbe(first); !errors.Is(err, ErrProbeSuperseded) {
		t.Fatalf("err = %v, want ErrProbeSuperseded", err)
	}
	if _, err := s.CompleteProbe(second); err != nil {
		t.Fatalf("CompleteProbe(second): %v", err)
	}
	if got := s.CurrentMTU(); got != 1400 {
		t.Fatalf("MTU = %d, want 1400", got)
	}
}

func TestProbeRaceDecreaseDuringFlight(t *testing.T) {
	s, _ := newTestState(t, Config{})
	n := sendPacket(t, s, make([]byte, 1500), 1400)
	if _, err := s.HandleNotification(n); err != nil {
		t.Fatalf("HandleNotification: %v", err)
	}
	tok, err := s.BeginProbe(1500)
	if err != nil {
		t.Fatalf("BeginProbe: %v", err)
	}
	// While the probe is in flight, a verified notification lowers the
	// MTU further; the probe's success must not undo it.
	n2 := sendPacket(t, s, make([]byte, 1400), 1300)
	if _, err := s.HandleNotification(n2); err != nil {
		t.Fatalf("HandleNotification: %v", err)
	}
	if _, err := s.CompleteProbe(tok); !errors.Is(err, ErrProbeSuperseded) {
		t.Fatalf("err = %v, want ErrProbeSuperseded", err)
	}
	if got := s.CurrentMTU(); got != 1300 {
		t.Fatalf("MTU = %d, want 1300", got)
	}
}

func TestProbeExpired(t *testing.T) {
	s, clock := newTestState(t, Config{})
	n := sendPacket(t, s, make([]byte, 1500), 1400)
	if _, err := s.HandleNotification(n); err != nil {
		t.Fatalf("HandleNotification: %v", err)
	}
	tok, err := s.BeginProbe(1500)
	if err != nil {
		t.Fatalf("BeginProbe: %v", err)
	}
	clock.Advance(4 * time.Second) // past the 3s probe timeout
	if _, err := s.CompleteProbe(tok); !errors.Is(err, ErrProbeExpired) {
		t.Fatalf("err = %v, want ErrProbeExpired", err)
	}
	if got := s.CurrentMTU(); got != 1400 {
		t.Fatalf("MTU = %d, want 1400", got)
	}
}

func TestProbeTokenFromOldPath(t *testing.T) {
	s, _ := newTestState(t, Config{})
	n := sendPacket(t, s, make([]byte, 1500), 1400)
	if _, err := s.HandleNotification(n); err != nil {
		t.Fatalf("HandleNotification: %v", err)
	}
	tok, err := s.BeginProbe(1500)
	if err != nil {
		t.Fatalf("BeginProbe: %v", err)
	}
	s.ResetPath()
	if _, err := s.CompleteProbe(tok); !errors.Is(err, ErrStalePath) {
		t.Fatalf("err = %v, want ErrStalePath", err)
	}
}

func TestProbeForgedSecret(t *testing.T) {
	s, _ := newTestState(t, Config{})
	n := sendPacket(t, s, make([]byte, 1500), 1400)
	if _, err := s.HandleNotification(n); err != nil {
		t.Fatalf("HandleNotification: %v", err)
	}
	tok, err := s.BeginProbe(1500)
	if err != nil {
		t.Fatalf("BeginProbe: %v", err)
	}
	tok.Secret++
	if _, err := s.CompleteProbe(tok); !errors.Is(err, ErrUnknownProbe) {
		t.Fatalf("err = %v, want ErrUnknownProbe", err)
	}
}

func TestMultiLayerEncapsulation(t *testing.T) {
	layers := []EncapLayer{
		{Name: "outer-ip", Overhead: 20},
		{Name: "udp", Overhead: 8},
		{Name: "wireguard", Overhead: 32},
		{Name: "inner-shim", Overhead: 16},
	}
	s, _ := newTestState(t, Config{LinkMTU: 1500, Layers: layers})
	if got := s.Overhead(); got != 76 {
		t.Fatalf("Overhead = %d, want 76", got)
	}
	if got := s.MaxPayloadMTU(); got != 1424 {
		t.Fatalf("MaxPayloadMTU = %d, want 1424", got)
	}
	// Floor derives from the 1280 minimum link MTU minus overhead.
	if got := s.MinPayloadMTU(); got != 1204 {
		t.Fatalf("MinPayloadMTU = %d, want 1204", got)
	}
	// A notification reports a wire MTU; the payload MTU subtracts the
	// full encapsulation stack.
	n := sendPacket(t, s, make([]byte, 1400), 1400)
	if _, err := s.HandleNotification(n); err != nil {
		t.Fatalf("HandleNotification: %v", err)
	}
	if got := s.CurrentMTU(); got != 1324 {
		t.Fatalf("MTU = %d, want 1324 (1400 wire - 76 overhead)", got)
	}
	// Wire size accounts for every layer.
	if _, err := s.OnSend(make([]byte, 1324)); err != nil {
		t.Fatalf("OnSend at current MTU: %v", err)
	}
	if _, err := s.OnSend(make([]byte, 1325)); !errors.Is(err, ErrPacketTooBig) {
		t.Fatalf("OnSend above current MTU: err = %v, want ErrPacketTooBig", err)
	}
}

func TestFloorClampsForgedTinyMTU(t *testing.T) {
	layers := []EncapLayer{
		{Name: "outer-ip", Overhead: 20},
		{Name: "udp", Overhead: 8},
		{Name: "wireguard", Overhead: 32},
	}
	s, _ := newTestState(t, Config{LinkMTU: 1500, Layers: layers})
	// Attacker forges a valid-looking notification with a tiny MTU to
	// make the tunnel unusable; the floor (1280-60=1220) clamps it.
	n := sendPacket(t, s, make([]byte, 1400), 68)
	ok, err := s.HandleNotification(n)
	if err != nil || !ok {
		t.Fatalf("HandleNotification = (%v, %v), want (true, nil)", ok, err)
	}
	if got := s.CurrentMTU(); got != 1220 {
		t.Fatalf("MTU = %d, want floor 1220", got)
	}
}

func TestOverheadExceedsLink(t *testing.T) {
	layers := []EncapLayer{{Name: "huge", Overhead: 1300}}
	if _, err := NewState(Config{LinkMTU: 1500, Layers: layers}); !errors.Is(err, ErrOverheadExceedsLink) {
		t.Fatalf("err = %v, want ErrOverheadExceedsLink", err)
	}
}

func TestBeginProbeBounds(t *testing.T) {
	s, _ := newTestState(t, Config{})
	if _, err := s.BeginProbe(1500); !errors.Is(err, ErrProbeTooSmall) {
		t.Fatalf("err = %v, want ErrProbeTooSmall", err)
	}
	if _, err := s.BeginProbe(1501); !errors.Is(err, ErrProbeTooLarge) {
		t.Fatalf("err = %v, want ErrProbeTooLarge", err)
	}
}
