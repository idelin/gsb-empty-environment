package acceptance_test

import (
	"errors"
	"testing"

	. "prescreen.local/ps-4b0c066dfd"
)

func mustOK(t *testing.T, r Result) {
	t.Helper()
	if r.Err != nil {
		t.Fatalf("unexpected event error: %v", r.Err)
	}
}

func requireLostNumbers(t *testing.T, r Result, want ...int64) {
	t.Helper()
	got := r.LostNumbers()
	if len(got) != len(want) {
		t.Fatalf("lost numbers = %v, want %v (result=%+v)", got, want, r)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("lost numbers = %v, want %v", got, want)
		}
	}
}

func TestPureACKIsNotBytesInFlightAndDoesNotArm(t *testing.T) {
	m := New(25_000)

	r := m.Send(SendEvent{Time: 1, Space: ApplicationData, PacketNumber: 0, Bytes: 1200, AckEliciting: true})
	mustOK(t, r)
	if r.BytesInFlight != 1200 {
		t.Fatalf("bytes after ack-eliciting send = %d, want 1200", r.BytesInFlight)
	}

	r = m.Send(SendEvent{Time: 2, Space: ApplicationData, PacketNumber: 1, Bytes: 97, PureACK: true})
	mustOK(t, r)
	if r.BytesInFlight != 1200 {
		t.Fatalf("bytes after pure ACK = %d, want 1200", r.BytesInFlight)
	}
}

func TestPacketThresholdLossAndLiveBytes(t *testing.T) {
	m := New(25_000)
	mustOK(t, m.SetHandshakeConfirmed(0, true))

	for pn := int64(0); pn < 4; pn++ {
		r := m.Send(SendEvent{
			Time:         0,
			Space:        ApplicationData,
			PacketNumber: pn,
			Bytes:        100,
			AckEliciting: true,
		})
		mustOK(t, r)
	}
	if got := m.BytesInFlight(); got != 400 {
		t.Fatalf("bytes in flight = %d, want 400", got)
	}

	r := m.Ack(AckEvent{
		Time:         10,
		Space:        ApplicationData,
		LargestAcked: 3,
	})
	mustOK(t, r)
	requireLostNumbers(t, r, 0)
	if r.BytesInFlight != 200 {
		t.Fatalf("bytes after ACK and packet loss = %d, want 200", r.BytesInFlight)
	}
	if r.NextTimeout() != 1000 {
		t.Fatalf("next timeout = %d, want loss deadline 1000", r.NextTimeout())
	}
	if r.Timer.Kind != LossTimer {
		t.Fatalf("timer kind = %s, want loss", r.Timer.Kind)
	}

	r = m.AdvanceClock(999)
	mustOK(t, r)
	if len(r.LostPackets) != 0 {
		t.Fatalf("premature losses at 999us: %+v", r.LostPackets)
	}
	if r.NextTimeout() != 1000 {
		t.Fatalf("next timeout after tick = %d, want 1000", r.NextTimeout())
	}

	r = m.AdvanceClock(1000)
	mustOK(t, r)
	requireLostNumbers(t, r, 1, 2)
	if r.BytesInFlight != 0 {
		t.Fatalf("bytes after time losses = %d, want 0", r.BytesInFlight)
	}
	if r.NextTimeout() != -1 {
		t.Fatalf("timer after all resolved = %d, want -1", r.NextTimeout())
	}
}

func TestTimeThresholdUsesNineEighthsRTT(t *testing.T) {
	m := New(25_000)
	mustOK(t, m.SetHandshakeConfirmed(0, true))

	mustOK(t, m.Send(SendEvent{Time: 0, Space: ApplicationData, PacketNumber: 0, Bytes: 100, AckEliciting: true}))
	r := m.Ack(AckEvent{Time: 80_000, Space: ApplicationData, LargestAcked: 0})
	mustOK(t, r)

	mustOK(t, m.Send(SendEvent{Time: 100_000, Space: ApplicationData, PacketNumber: 1, Bytes: 100, AckEliciting: true}))
	mustOK(t, m.Send(SendEvent{Time: 100_001, Space: ApplicationData, PacketNumber: 2, Bytes: 100, AckEliciting: true}))
	r = m.Ack(AckEvent{Time: 180_001, Space: ApplicationData, LargestAcked: 2})
	mustOK(t, r)

	if latest, srtt, rttvar := r.RTTTriple(); latest != 80_000 || srtt != 80_000 || rttvar != 30_000 {
		t.Fatalf("RTT triple = (%d,%d,%d), want (80000,80000,30000)", latest, srtt, rttvar)
	}
	if r.NextTimeout() != 190_000 {
		t.Fatalf("loss deadline = %d, want 190000", r.NextTimeout())
	}

	r = m.AdvanceClock(189_999)
	if len(r.LostPackets) != 0 {
		t.Fatalf("packet lost %dus too early", 1)
	}
	r = m.AdvanceClock(190_000)
	requireLostNumbers(t, r, 1)
	if r.LostPackets[0].Reason != TimeThresholdLoss {
		t.Fatalf("loss reason = %s, want time_threshold", r.LostPackets[0].Reason)
	}
}

func TestOutOfOrderAckDoesNotRewindLargestAndRangesAckLowerPackets(t *testing.T) {
	m := New(25_000)
	mustOK(t, m.SetHandshakeConfirmed(0, true))
	for pn := int64(0); pn < 3; pn++ {
		mustOK(t, m.Send(SendEvent{Time: 0, Space: ApplicationData, PacketNumber: pn, Bytes: 100, AckEliciting: true}))
	}

	r := m.Ack(AckEvent{Time: 1000, Space: ApplicationData, LargestAcked: 2})
	mustOK(t, r)
	if r.LargestAcked != 2 {
		t.Fatalf("largest = %d, want 2", r.LargestAcked)
	}

	// This late ACK has a smaller largest_acked but newly acknowledges 0 and 1.
	r = m.Ack(AckEvent{
		Time:         1100,
		Space:        ApplicationData,
		LargestAcked: 1,
		Ranges:       []AckRange{{First: 0, Last: 1}},
	})
	mustOK(t, r)
	if r.LargestAcked != 2 {
		t.Fatalf("largest rewound to %d, want 2", r.LargestAcked)
	}
	if r.BytesInFlight != 0 {
		t.Fatalf("bytes = %d, want 0", r.BytesInFlight)
	}
	if latest := r.RTT.LatestRTT; latest != 1000 {
		t.Fatalf("late reordered ACK changed latest RTT to %d", latest)
	}

	// Multiple disjoint ranges acknowledge only packets 1 and 4.
	m = New(25_000)
	mustOK(t, m.SetHandshakeConfirmed(0, true))
	for pn := int64(0); pn < 5; pn++ {
		mustOK(t, m.Send(SendEvent{Time: 0, Space: ApplicationData, PacketNumber: pn, Bytes: 100, AckEliciting: true}))
	}
	r = m.Ack(AckEvent{
		Time:         100,
		Space:        ApplicationData,
		LargestAcked: 4,
		Ranges:       []AckRange{Range(4, 4), Range(1, 1)},
	})
	mustOK(t, r)
	requireLostNumbers(t, r, 0)
	if r.BytesInFlight != 200 {
		t.Fatalf("bytes after disjoint ACK and threshold loss = %d, want 200", r.BytesInFlight)
	}
}

func TestAckDelayProtectionAndApplicationSpaceOnly(t *testing.T) {
	m := New(25_000)
	mustOK(t, m.SetHandshakeConfirmed(0, true))
	mustOK(t, m.Send(SendEvent{Time: 0, Space: ApplicationData, PacketNumber: 0, Bytes: 100, AckEliciting: true}))
	r := m.Ack(AckEvent{Time: 100_000, Space: ApplicationData, LargestAcked: 0, AckDelay: 0})
	mustOK(t, r)

	mustOK(t, m.Send(SendEvent{Time: 200_000, Space: ApplicationData, PacketNumber: 1, Bytes: 100, AckEliciting: true}))
	r = m.Ack(AckEvent{
		Time:         310_000,
		Space:        ApplicationData,
		LargestAcked: 1,
		AckDelay:     100_000, // exceeds peer max_ack_delay and latest-min_rtt.
	})
	mustOK(t, r)
	if got := r.RTT; got.LatestRTT != 110_000 || got.MinRTT != 100_000 ||
		got.SmoothedRTT != 101_250 || got.RTTVar != 40_000 {
		t.Fatalf("protected RTT state = %+v", got)
	}

	// Initial/Handshake ACKs do not create RTT samples under the requested rule.
	h := New(25_000)
	mustOK(t, h.Send(SendEvent{Time: 0, Space: Handshake, PacketNumber: 0, Bytes: 1200, AckEliciting: true}))
	r = h.Ack(AckEvent{Time: 100_000, Space: Handshake, LargestAcked: 0, AckDelay: 10_000})
	mustOK(t, r)
	if r.RTT.HasRTTSample {
		t.Fatalf("handshake ACK updated RTT: %+v", r.RTT)
	}
	if r.RTT.SmoothedRTT != InitialRTT || r.RTT.RTTVar != InitialRTT/2 {
		t.Fatalf("initial RTT estimator changed: %+v", r.RTT)
	}
}

func TestPTOSpaceSwitchesAfterHandshakeConfirmation(t *testing.T) {
	m := New(25_000)

	r := m.Send(SendEvent{Time: 0, Space: ApplicationData, PacketNumber: 0, Bytes: 1200, AckEliciting: true})
	mustOK(t, r)
	r = m.Send(SendEvent{Time: 100_000, Space: Handshake, PacketNumber: 0, Bytes: 1200, AckEliciting: true})
	mustOK(t, r)
	if r.Timer.Space != Handshake || r.NextTimeout() != 1_099_000 {
		t.Fatalf("pre-confirm PTO = %s@%d, want Handshake@1099000", r.Timer.Space, r.NextTimeout())
	}

	r = m.SetHandshakeConfirmed(200_000, true)
	mustOK(t, r)
	if r.Timer.Space != ApplicationData || r.NextTimeout() != 1_024_000 {
		t.Fatalf("post-confirm PTO = %s@%d, want ApplicationData@1024000", r.Timer.Space, r.NextTimeout())
	}
}

func TestNoAckElicitingInFlightAntiAmplificationAndClockPTO(t *testing.T) {
	m := New(25_000)
	limited := true
	r := m.PathValidation(PathEvent{Time: 0, AntiAmplificationLimited: &limited})
	mustOK(t, r)
	if r.NextTimeout() != -1 {
		t.Fatalf("anti-amplification-limited timer = %d, want -1", r.NextTimeout())
	}

	limited = false
	r = m.PathValidation(PathEvent{Time: 1000, AntiAmplificationLimited: &limited})
	mustOK(t, r)
	if r.Timer.Space != Initial || r.NextTimeout() != 1_000_000 {
		t.Fatalf("unblocked fallback PTO = %s@%d, want Initial@1000000", r.Timer.Space, r.NextTimeout())
	}

	r = m.AdvanceClock(1_000_000)
	mustOK(t, r)
	if r.PTOFired == nil || r.PTOFired.Space != Initial {
		t.Fatalf("PTO fired ref = %+v, want Initial", r.PTOFired)
	}
	if r.Timer.PTOCount != 1 || r.Timer.BackoffFactor != 2 || r.NextTimeout() != 2_998_000 {
		t.Fatalf("backed-off timer = %+v deadline=%d", r.Timer, r.NextTimeout())
	}

	r = m.SetHandshakeConfirmed(3_000_000, true)
	mustOK(t, r)
	if r.NextTimeout() != -1 {
		t.Fatalf("confirmed and no AE in flight timer = %d, want -1", r.NextTimeout())
	}
}

func TestUnifiedHandleRejectsMonotonicViolation(t *testing.T) {
	m := New(25_000)
	m.Handle(Clock(10))
	r := m.Handle(Clock(9))
	if !errors.Is(r.Err, ErrNonMonotonicTime) {
		t.Fatalf("error = %v, want ErrNonMonotonicTime", r.Err)
	}
}
