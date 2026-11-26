// Package acceptance_test exercises the RFC 9002 state machine against the
// required acceptance scenarios. It is an external test package so it also
// proves the root package is importable.
package acceptance_test

import (
	"testing"
	"time"

	rfc9002 "prescreen.local/ps-8dea28f4a0"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

const mss = 1200

func newTestDetector(t *testing.T) *rfc9002.LossDetector {
	t.Helper()
	ld := rfc9002.NewLossDetector()
	return ld
}

func lostNumbers(out *rfc9002.AckOutcome) []rfc9002.PacketNumber {
	ns := make([]rfc9002.PacketNumber, 0, len(out.Lost))
	for _, l := range out.Lost {
		ns = append(ns, l.Number)
	}
	return ns
}

func containsNumber(ns []rfc9002.PacketNumber, want rfc9002.PacketNumber) bool {
	for _, n := range ns {
		if n == want {
			return true
		}
	}
	return false
}

// 1. Cross-three-space out-of-order ACKs with independent packet numbers and
// the packet threshold.
func TestCrossSpaceOutOfOrderAcks(t *testing.T) {
	ld := newTestDetector(t)

	// Five packets numbered 0..4 in EVERY space (numbering is independent).
	for _, sp := range rfc9002.AllSpaces {
		for n := rfc9002.PacketNumber(0); n < 5; n++ {
			ld.SendAckEliciting(sp, n, t0, mss)
		}
	}
	if got := ld.BytesInFlight; got != 3*5*mss {
		t.Fatalf("bytes_in_flight after sends = %d, want %d", got, 3*5*mss)
	}

	// Cumulative ACK of ApplicationData 0..4; this must not touch the other
	// two spaces (exact threshold semantics are checked on ld2 below).
	out := ld.AckUpTo(rfc9002.ApplicationData, 4, t0.Add(5*time.Millisecond))
	if len(out.NewlyAcked) != 5 {
		// AckUpTo cumulatively acks every outstanding <= 4, i.e. all five.
		t.Fatalf("cumulative ack newly acked = %v, want all 0..4", out.NewlyAcked)
	}

	// Use a non-cumulative ACK instead for precise threshold semantics on a
	// fresh detector below; here just assert no cross-space contamination.
	if _, ok := ld.LargestAckedFor(rfc9002.Initial); ok {
		t.Fatal("Initial largest_acked must remain unset after AppData ACK")
	}
	if _, ok := ld.LargestAckedFor(rfc9002.Handshake); ok {
		t.Fatal("Handshake largest_acked must remain unset after AppData ACK")
	}
	if _, ok := ld.LargestAckedFor(rfc9002.ApplicationData); !ok {
		t.Fatal("ApplicationData largest_acked must be set")
	}
	if got := ld.InFlightForSpace(rfc9002.Initial); got != 5*mss {
		t.Fatalf("Initial in-flight leaked = %d, want %d", got, 5*mss)
	}
	if got := ld.InFlightForSpace(rfc9002.Handshake); got != 5*mss {
		t.Fatalf("Handshake in-flight leaked = %d, want %d", got, 5*mss)
	}

	// Fresh detector for exact, non-cumulative out-of-order ACK behavior.
	ld2 := newTestDetector(t)
	for _, sp := range rfc9002.AllSpaces {
		for n := rfc9002.PacketNumber(0); n < 7; n++ {
			ld2.SendAckEliciting(sp, n, t0, mss)
		}
	}

	// Out-of-order / selective ACK: only packet 6 in Initial first.
	o1 := ld2.AckOne(rfc9002.Initial, 6, t0.Add(5*time.Millisecond))
	gotLost := lostNumbers(o1)
	for _, want := range []rfc9002.PacketNumber{0, 1, 2, 3} {
		if !containsNumber(gotLost, want) {
			t.Fatalf("Initial first ACK lost %v, want to include %d", gotLost, want)
		}
	}
	if len(gotLost) != 4 {
		t.Fatalf("Initial first ACK lost exactly %v (len 4), got %v", []int{0, 1, 2, 3}, gotLost)
	}

	// Other spaces must be completely untouched by the Initial ACK.
	if _, ok := ld2.LargestAckedFor(rfc9002.Handshake); ok {
		t.Fatal("Handshake touched by Initial ACK")
	}
	if ld2.InFlightForSpace(rfc9002.ApplicationData) != 7*mss {
		t.Fatal("ApplicationData in-flight changed by Initial ACK")
	}

	// Delayed/out-of-order ACK for 4 and 5 acknowledges them; 6 duplicate
	// does not generate a new RTT sample.
	beforeSamples := ld2.Samples
	o2 := ld2.AckNumbers(rfc9002.Initial, []rfc9002.PacketNumber{4, 5, 6}, t0.Add(8*time.Millisecond))
	if !containsNumber(o2.NewlyAcked, 4) || !containsNumber(o2.NewlyAcked, 5) {
		t.Fatalf("delayed ACK newly acked %v, want 4 and 5", o2.NewlyAcked)
	}
	if containsNumber(o2.NewlyAcked, 6) {
		t.Fatal("packet 6 must not be newly acked twice")
	}
	if ld2.Samples != beforeSamples {
		t.Fatalf("duplicate largest ACK updated RTT: samples %d -> %d", beforeSamples, ld2.Samples)
	}

	// Same independent threshold in Handshake does not know about Initial.
	o3 := ld2.AckOne(rfc9002.Handshake, 4, t0.Add(9*time.Millisecond))
	hsLost := lostNumbers(o3)
	if len(hsLost) != 2 || !containsNumber(hsLost, 0) || !containsNumber(hsLost, 1) {
		t.Fatalf("Handshake threshold loss = %v, want [0 1]", hsLost)
	}
}

// Time threshold produces a loss_time deadline that drives the unified timer.
func TestTimeThresholdAndUnifiedTimer(t *testing.T) {
	ld := newTestDetector(t)
	for n := rfc9002.PacketNumber(0); n < 5; n++ {
		ld.SendAckEliciting(rfc9002.Handshake, n, t0, mss)
	}
	// First ACK establishes a tiny RTT; 0,1 are packet-threshold lost.
	out := ld.AckOne(rfc9002.Handshake, 4, t0.Add(5*time.Millisecond))
	if len(out.Lost) != 2 {
		t.Fatalf("packet-threshold losses = %d, want 2", len(out.Lost))
	}
	if out.Timer.Kind != rfc9002.TimerLoss || !out.Timer.Armed {
		t.Fatalf("timer = %+v, want armed loss timer", out.Timer)
	}
	lossDeadline, sp := ld.GetLossTimeAndSpace()
	if lossDeadline.IsZero() || sp != rfc9002.Handshake {
		t.Fatalf("GetLossTimeAndSpace = %v,%s", lossDeadline, sp)
	}

	// Fire the loss timer late enough for the time threshold to claim 2,3.
	to := ld.OnLossDetectionTimeout(lossDeadline.Add(time.Millisecond))
	if to.Kind != rfc9002.TimerLoss {
		t.Fatalf("timeout kind = %s, want loss", to.Kind)
	}
	if len(to.Lost) != 2 {
		t.Fatalf("time-threshold losses = %v, want 2 packets", lostNumbers(&rfc9002.AckOutcome{Lost: to.Lost}))
	}
}

// 2. Anti-amplification lock-up and release.
func TestAntiAmplificationLockAndRelease(t *testing.T) {
	ld := rfc9002.NewServer(rfc9002.WithMaxAckDelay(25 * time.Millisecond))

	// Client Initial arrives: 1200 received bytes.
	ld.RecordBytesReceived(1200)

	// Server emits three full-sized Initial packets = 3600 sent bytes,
	// exactly the 3x amplification budget.
	for n := rfc9002.PacketNumber(0); n < 3; n++ {
		ld.SendAckEliciting(rfc9002.Initial, n, t0, mss)
	}
	if !ld.AntiAmplificationLimited() {
		t.Fatal("server should be at the 3x anti-amplification limit")
	}

	// Raw RFC PTO calculation still has an anchor ...
	if d, sp := ld.GetPtoTimeAndSpace(); d.IsZero() || sp != rfc9002.Initial {
		t.Fatalf("raw GetPtoTimeAndSpace = %v,%s, want Initial deadline", d, sp)
	}
	// ... but the unified timer must be disabled with the block reason.
	ts := ld.SetLossDetectionTimer(t0)
	if ts.Armed || !ts.AmplificationBlocked || ts.Kind != rfc9002.TimerNone {
		t.Fatalf("timer under amp limit = %+v, want disarmed/blocked", ts)
	}
	if d, _, blocked := ld.GetEffectivePtoTimeAndSpace(t0); !blocked || !d.IsZero() {
		t.Fatalf("effective PTO = %v blocked=%v, want zero/blocked", d, blocked)
	}

	// Repeated timer expirations while blocked must not count PTO or emit
	// probes (no PTO death loop).
	for i := 0; i < 5; i++ {
		to := ld.OnLossDetectionTimeout(t0.Add(time.Duration(i+1) * time.Second))
		if to.PTOFired || !to.AmplificationBlocked {
			t.Fatalf("blocked timeout iteration %d = %+v, want blocked no-PTO", i, to)
		}
	}
	if ld.PTOCount != 0 || ld.PTOExpirations != 0 {
		t.Fatalf("PTO spun while blocked: count=%d expirations=%d", ld.PTOCount, ld.PTOExpirations)
	}

	// A new client packet opens the budget and re-arms the PTO.
	armed := ld.ReceiveClientPacket(1200, t0.Add(2*time.Second))
	if ld.AntiAmplificationLimited() {
		t.Fatal("limit should be lifted after new client bytes")
	}
	if !armed.Armed || armed.Kind != rfc9002.TimerPTO || armed.Space != rfc9002.Initial {
		t.Fatalf("timer after release = %+v, want armed Initial PTO", armed)
	}
	// RFC 9002 5.3 init: SRTT=kInitialRtt(333ms), RTTVar=kInitialRtt/2, so
	// PTO = 333 + max(4*166.5, 1) = 999ms (= 3*kInitialRtt).
	wantPTO := t0.Add(999 * time.Millisecond)
	if !armed.Deadline.Equal(wantPTO) {
		t.Fatalf("PTO deadline = %v, want %v", armed.Deadline, wantPTO)
	}

	// Address validation also clears the block even without more bytes.
	ld2 := rfc9002.NewServer()
	ld2.RecordBytesReceived(1000)
	ld2.RecordBytesSent(3000)
	if !ld2.AntiAmplificationLimited() {
		t.Fatal("expected amp limit on ld2")
	}
	st := ld2.ValidateAddress(t0)
	if ld2.AntiAmplificationLimited() || st.AmplificationBlocked {
		t.Fatal("address validation must clear amp limit")
	}
}

// 3. max_ack_delay is only subtracted for Application Data after handshake
// confirmation, and capped/bounded.
func TestMaxAckDelayGatingBeforeAfterConfirm(t *testing.T) {
	build := func(confirmed bool, space rfc9002.PacketNumberSpace) *rfc9002.LossDetector {
		ld := rfc9002.NewLossDetector(rfc9002.WithMaxAckDelay(25 * time.Millisecond))
		ld.HandshakeConfirmed = confirmed
		ackAt := t0
		for i := 0; i <= 30; i++ {
			latest := 100 * time.Millisecond
			if i == 0 {
				latest = 20 * time.Millisecond // pins min_rtt to 20ms
			}
			ackAt = ackAt.Add(time.Second)
			pn := rfc9002.PacketNumber(i)
			ld.SendAckEliciting(space, pn, ackAt.Add(-latest), mss)
			ld.OnAckReceived(rfc9002.AckFrame{
				Space:        space,
				LargestAcked: pn,
				AckDelay:     25 * time.Millisecond,
			}, ackAt)
		}
		return ld
	}

	before := build(false, rfc9002.ApplicationData)
	after := build(true, rfc9002.ApplicationData)
	initial := build(true, rfc9002.Initial) // confirmed but wrong space

	if before.SmoothedRTT < 95*time.Millisecond {
		t.Fatalf("pre-confirm smoothed_rtt = %v, want ~100ms (no ack delay subtraction)", before.SmoothedRTT)
	}
	if after.SmoothedRTT > 80*time.Millisecond || after.SmoothedRTT < 68*time.Millisecond {
		t.Fatalf("post-confirm smoothed_rtt = %v, want converged near 75ms", after.SmoothedRTT)
	}
	if initial.SmoothedRTT < 95*time.Millisecond {
		t.Fatalf("Initial-space smoothed_rtt = %v, want ~100ms (max_ack_delay never applies)", initial.SmoothedRTT)
	}
	if after.SmoothedRTT >= before.SmoothedRTT {
		t.Fatalf("post-confirm SRTT %v should be below pre-confirm %v", after.SmoothedRTT, before.SmoothedRTT)
	}
}

func TestRTTSampleEligibilityAndClamp(t *testing.T) {
	ld := rfc9002.NewLossDetector()

	// Largest newly acked + ack-eliciting => sample.
	ld.SendAckEliciting(rfc9002.Handshake, 0, t0.Add(-50*time.Millisecond), mss)
	out := ld.AckOne(rfc9002.Handshake, 0, t0)
	if !out.RTTUpdated || ld.Samples != 1 {
		t.Fatalf("first RTT sample not taken: out=%+v samples=%d", out.RTTUpdated, ld.Samples)
	}

	// Duplicate ACK where largest is not newly acked => no sample update.
	out2 := ld.AckOne(rfc9002.Handshake, 0, t0.Add(500*time.Millisecond))
	if out2.RTTUpdated || ld.Samples != 1 || ld.LatestRTT != 50*time.Millisecond {
		t.Fatalf("duplicate ACK changed RTT: updated=%v samples=%d latest=%v", out2.RTTUpdated, ld.Samples, ld.LatestRTT)
	}

	// Newly acked largest is a pure ACK and no other newly acked AE packet
	// exists => no RTT sample.
	ld.SendPureAck(rfc9002.Handshake, 1, t0.Add(500*time.Millisecond), 60)
	out3 := ld.AckOne(rfc9002.Handshake, 1, t0.Add(510*time.Millisecond))
	if out3.RTTUpdated || ld.Samples != 1 {
		t.Fatalf("pure-ACK largest produced RTT sample: %+v samples=%d", out3.RTTUpdated, ld.Samples)
	}

	// Direct clamp/cap check: min=20ms, latest=40ms => bound 20ms; a huge
	// ack_delay is capped by max_ack_delay then clamped to 20ms, so the
	// adjusted RTT equals min_rtt (20ms) and never goes below.
	r := &rfc9002.RTTEstimator{}
	r.Update(20*time.Millisecond, 0, 25*time.Millisecond, true)
	r.Update(40*time.Millisecond, time.Second, 25*time.Millisecond, true)
	if r.MinRTT != 20*time.Millisecond {
		t.Fatalf("min_rtt = %v, want 20ms", r.MinRTT)
	}
	wantSRTT := (7*20 + 20) * time.Millisecond / 8
	if r.SmoothedRTT != wantSRTT {
		t.Fatalf("clamped smoothed = %v, want %v", r.SmoothedRTT, wantSRTT)
	}
}

// 4. Pure ACK packets do not count in flight and do not arm PTO.
func TestPureAckDoesNotTriggerPTO(t *testing.T) {
	ld := rfc9002.NewLossDetector()

	ld.SendPureAck(rfc9002.Initial, 0, t0, 60)
	ld.SendPureAck(rfc9002.Initial, 1, t0.Add(10*time.Millisecond), 60)
	if ld.BytesInFlight != 0 || ld.InFlightForSpace(rfc9002.Initial) != 0 {
		t.Fatalf("pure ACK counted in flight: total=%d initial=%d", ld.BytesInFlight, ld.InFlightForSpace(rfc9002.Initial))
	}
	if d, _ := ld.GetPtoTimeAndSpace(); !d.IsZero() {
		t.Fatalf("PTO armed by pure ACKs at %v", d)
	}
	if ts := ld.SetLossDetectionTimer(t0); ts.Armed {
		t.Fatalf("timer armed for pure-ACK-only endpoint: %+v", ts)
	}
	if to := ld.OnLossDetectionTimeout(t0.Add(time.Hour)); to.PTOFired {
		t.Fatal("PTO fired with only pure ACKs sent")
	}

	// An ack-eliciting packet arms PTO anchored at its send time, and a later
	// pure ACK does not move the anchor.
	ld.SendAckEliciting(rfc9002.Initial, 2, t0, mss)
	ld.SendPureAck(rfc9002.Initial, 3, t0.Add(50*time.Millisecond), 60)
	if ld.BytesInFlight != mss {
		t.Fatalf("bytes_in_flight = %d, want %d", ld.BytesInFlight, mss)
	}
	d, sp := ld.GetPtoTimeAndSpace()
	if d.IsZero() || sp != rfc9002.Initial {
		t.Fatalf("PTO = %v,%s after AE send", d, sp)
	}
	want := t0.Add(999 * time.Millisecond) // 3*kInitialRtt under RFC 5.3 init
	if !d.Equal(want) {
		t.Fatalf("PTO anchored at %v, want %v (pure ACK must not re-anchor)", d, want)
	}
}

// 5. Discarding Initial/Handshake keys cleans per-space in-flight bytes and
// loss timers and resets pto_count.
func TestKeyDiscardCleanup(t *testing.T) {
	ld := rfc9002.NewLossDetector()
	for n := rfc9002.PacketNumber(0); n < 2; n++ {
		ld.SendAckEliciting(rfc9002.Initial, n, t0, mss)
		ld.SendAckEliciting(rfc9002.Handshake, n, t0, mss)
	}
	for n := rfc9002.PacketNumber(0); n < 3; n++ {
		ld.SendAckEliciting(rfc9002.ApplicationData, n, t0, mss)
	}
	if ld.BytesInFlight != 7*mss {
		t.Fatalf("bytes = %d, want %d", ld.BytesInFlight, 7*mss)
	}
	ld.PTOCount = 3
	ld.Spaces[rfc9002.Initial].LossTime = t0.Add(5 * time.Millisecond)

	out := ld.DiscardKeys(rfc9002.Initial, t0)
	if !out.Discarded || out.BytesRemoved != 2*mss || out.PacketsRemoved != 2 {
		t.Fatalf("Initial discard = %+v", out)
	}
	if !out.PTOCountReset || ld.PTOCount != 0 {
		t.Fatalf("pto_count not reset: out=%+v count=%d", out.PTOCountReset, ld.PTOCount)
	}
	if ld.InFlightForSpace(rfc9002.Initial) != 0 {
		t.Fatal("Initial in-flight bytes remain after discard")
	}
	if len(ld.SentPacketsFor(rfc9002.Initial)) != 0 {
		t.Fatal("Initial sent table not cleared")
	}
	if !ld.Spaces[rfc9002.Initial].LossTime.IsZero() {
		t.Fatal("Initial loss timer not cleared")
	}
	if ld.BytesInFlight != 5*mss {
		t.Fatalf("total bytes after Initial discard = %d, want %d", ld.BytesInFlight, 5*mss)
	}
	if out.Timer.Kind != rfc9002.TimerPTO || out.Timer.Space != rfc9002.Handshake {
		t.Fatalf("timer after Initial discard = %+v, want Handshake PTO", out.Timer)
	}

	// Sending/acking into a discarded space is a safe no-op.
	if p := ld.SendAckEliciting(rfc9002.Initial, 9, t0, mss); p != nil {
		t.Fatal("send into discarded Initial space should be ignored")
	}
	ld.AckOne(rfc9002.Initial, 9, t0)

	// Discard Handshake too: only Application Data bytes remain, but AppData
	// PTO is skipped until handshake confirmation.
	out2 := ld.DiscardKeys(rfc9002.Handshake, t0)
	if out2.BytesRemoved != 2*mss {
		t.Fatalf("Handshake bytes removed = %d, want %d", out2.BytesRemoved, 2*mss)
	}
	if ld.BytesInFlight != 3*mss {
		t.Fatalf("total after HS discard = %d, want %d", ld.BytesInFlight, 3*mss)
	}
	if out2.Timer.Armed {
		t.Fatalf("timer should be disarmed with only unconfirmed AppData: %+v", out2.Timer)
	}

	ts := ld.ConfirmHandshake(t0)
	if ts.Kind != rfc9002.TimerPTO || ts.Space != rfc9002.ApplicationData {
		t.Fatalf("after confirm timer = %+v, want AppData PTO", ts)
	}

	// Idempotent discard.
	again := ld.DiscardKeys(rfc9002.Initial, t0)
	if again.Discarded {
		t.Fatal("double discard should be idempotent no-op")
	}
}

// ECN-CE growth halves at most once per recovery epoch, like loss.
func TestECNCEOncePerRecoveryEpoch(t *testing.T) {
	ld := rfc9002.NewLossDetector()
	initialCWND := ld.CongestionWindow

	if !ld.OnECNCE(1, t0) {
		t.Fatal("first ECN-CE increase must halve the window")
	}
	want := initialCWND / 2
	if ld.CongestionWindow != want {
		t.Fatalf("cwnd after ECN = %d, want %d", ld.CongestionWindow, want)
	}
	if ld.OnECNCE(4, t0) {
		t.Fatal("second ECN-CE growth in same epoch must not halve again")
	}
	if ld.CongestionReductions != 1 || ld.CongestionWindow != want {
		t.Fatalf("epoch not collapsed: reductions=%d cwnd=%d", ld.CongestionReductions, ld.CongestionWindow)
	}
	if ld.CongestionEvent(t0) {
		t.Fatal("loss in same epoch must also be suppressed")
	}

	ld.EndRecovery()
	if !ld.OnECNCE(1, t0.Add(time.Second)) {
		t.Fatal("ECN after ending recovery epoch must halve again")
	}
	if ld.CongestionReductions != 2 || ld.CongestionWindow != want/2 {
		t.Fatalf("second epoch reductions=%d cwnd=%d, want 2/%d", ld.CongestionReductions, ld.CongestionWindow, want/2)
	}

	// Absolute counter ingestion with duplicate values.
	ld2 := rfc9002.NewLossDetector()
	if !ld2.OnECNCECount(1, t0) {
		t.Fatal("CE counter 0->1 should reduce")
	}
	if ld2.OnECNCECount(1, t0) {
		t.Fatal("unchanged CE counter must not reduce")
	}
	if ld2.OnECNCECount(2, t0) {
		t.Fatal("CE growth within same epoch must be suppressed")
	}
	ld2.EndRecovery()
	if !ld2.OnECNCECount(3, t0.Add(time.Second)) {
		t.Fatal("CE growth in a new epoch should reduce")
	}
}

// 6. Persistent congestion resets cwnd to minimum and ACKs then grow it
// (recovery).
func TestPersistentCongestionResetAndRecovery(t *testing.T) {
	cfg := rfc9002.DefaultConfig()
	cfg.CongestionWindow = 10000
	cfg.MinimumWindow = 1000
	ld := rfc9002.NewWithConfig(cfg)

	if !ld.CongestionEvent(t0) {
		t.Fatal("first congestion event should halve")
	}
	if ld.CongestionWindow != 5000 {
		t.Fatalf("halved cwnd = %d, want 5000", ld.CongestionWindow)
	}
	if ld.CongestionEvent(t0) {
		t.Fatal("repeat event in same epoch must be suppressed")
	}

	ld.OnPersistentCongestion(t0)
	if ld.CongestionWindow != 1000 {
		t.Fatalf("persistent cwnd = %d, want minimum 1000", ld.CongestionWindow)
	}
	if !ld.PersistentCongestion || ld.PersistentCongestionCount != 1 {
		t.Fatal("persistent congestion state not latched")
	}

	// New recovery packets sent AFTER the event are not in the recovery
	// epoch and grow cwnd via slow start.
	sendAndAck := func(pn rfc9002.PacketNumber, at time.Time, size int) {
		ld.SendAckEliciting(rfc9002.ApplicationData, pn, at, size)
		ld.AckOne(rfc9002.ApplicationData, pn, at.Add(10*time.Millisecond))
	}
	sendAndAck(10, t0.Add(time.Second), 1000)
	if ld.CongestionWindow != 2000 {
		t.Fatalf("cwnd after first recovery ACK = %d, want 2000", ld.CongestionWindow)
	}
	sendAndAck(11, t0.Add(2*time.Second), 1000)
	if ld.CongestionWindow != 3000 {
		t.Fatalf("cwnd after second recovery ACK = %d, want 3000", ld.CongestionWindow)
	}
}

// Persistent congestion is also detected automatically from a long interval
// of lost packets.
func TestAutomaticPersistentCongestionDetection(t *testing.T) {
	cfg := rfc9002.DefaultConfig()
	cfg.MaxAckDelay = 25 * time.Millisecond
	ld := rfc9002.NewWithConfig(cfg)

	// Pin RTT so persistent congestion duration is exactly 378ms:
	// (100ms + max(0, 1ms) + 25ms) * 3.
	ld.SmoothedRTT = 100 * time.Millisecond
	ld.LatestRTT = 100 * time.Millisecond
	ld.MinRTT = 100 * time.Millisecond
	ld.RTTVar = 0
	ld.Samples = 4
	if d := ld.PersistentCongestionDuration(); d != 378*time.Millisecond {
		t.Fatalf("PC duration = %v, want 378ms", d)
	}

	offsets := []time.Duration{0, 100, 200, 500}
	for i, off := range offsets {
		ld.SendAckEliciting(rfc9002.ApplicationData, rfc9002.PacketNumber(i+1), t0.Add(off*time.Millisecond), mss)
	}
	st := &ld.Spaces[rfc9002.ApplicationData]
	st.LargestAcked = 50
	st.LargestAckedValid = true

	minWindow := ld.MinimumWindow
	lost := ld.DetectLostPackets(rfc9002.ApplicationData, t0.Add(900*time.Millisecond))
	if len(lost) != 4 {
		t.Fatalf("lost = %d, want 4", len(lost))
	}
	if ld.PersistentCongestionCount != 1 {
		t.Fatalf("persistent count = %d, want 1", ld.PersistentCongestionCount)
	}
	if ld.CongestionWindow != minWindow {
		t.Fatalf("cwnd after automatic PC = %d, want min %d", ld.CongestionWindow, minWindow)
	}
}

// End-to-end ACK-driven loss must halve at most once across several loss
// detections belonging to the same recovery epoch.
func TestLossHalvingOncePerEpochEndToEnd(t *testing.T) {
	ld := rfc9002.NewLossDetector()
	for n := rfc9002.PacketNumber(0); n < 10; n++ {
		ld.SendAckEliciting(rfc9002.ApplicationData, n, t0, mss)
	}

	o1 := ld.AckOne(rfc9002.ApplicationData, 3, t0.Add(5*time.Millisecond))
	if !o1.CongestionReduced || ld.CongestionReductions != 1 {
		t.Fatalf("first loss should halve: outcome reduced=%v total=%d", o1.CongestionReduced, ld.CongestionReductions)
	}
	cwndAfterFirst := ld.CongestionWindow

	// A later ACK detects more old packets lost, but they were sent before
	// the recovery epoch opened: no second halving.
	o2 := ld.AckOne(rfc9002.ApplicationData, 6, t0.Add(6*time.Millisecond))
	if o2.CongestionReduced {
		t.Fatal("second loss batch in same epoch must not halve again")
	}
	if ld.CongestionReductions != 1 || ld.CongestionWindow != cwndAfterFirst {
		t.Fatalf("cwnd/reductions changed in epoch: reductions=%d cwnd=%d (want %d)",
			ld.CongestionReductions, ld.CongestionWindow, cwndAfterFirst)
	}

	// A post-epoch packet that is later lost opens a fresh epoch. Keep the
	// send gap short so this is not classified as persistent congestion.
	newSend := t0.Add(8 * time.Millisecond)
	for n := rfc9002.PacketNumber(20); n <= 24; n++ {
		ld.SendAckEliciting(rfc9002.ApplicationData, n, newSend, mss)
	}
	o3 := ld.AckOne(rfc9002.ApplicationData, 23, newSend.Add(5*time.Millisecond))
	if !o3.CongestionReduced || ld.CongestionReductions != 2 {
		t.Fatalf("post-epoch loss should start a new halving: reduced=%v total=%d",
			o3.CongestionReduced, ld.CongestionReductions)
	}
	if ld.CongestionWindow >= cwndAfterFirst {
		t.Fatalf("cwnd %d should decrease from %d in new epoch", ld.CongestionWindow, cwndAfterFirst)
	}
}

// PTO doubles with pto_count, includes max_ack_delay only for AppData, and
// resets on forward-progress ACK.
func TestPTOBackoffAndMaxAckDelaySpace(t *testing.T) {
	ld := rfc9002.NewLossDetector(rfc9002.WithMaxAckDelay(25 * time.Millisecond))
	ld.SendAckEliciting(rfc9002.Initial, 0, t0, mss)

	// No RTT sample: RFC 5.3 init gives PTO = 3*kInitialRtt = 999ms.
	d0, sp := ld.GetPtoTimeAndSpace()
	if sp != rfc9002.Initial || !d0.Equal(t0.Add(999*time.Millisecond)) {
		t.Fatalf("initial PTO = %v,%s want %v,initial", d0, sp, t0.Add(999*time.Millisecond))
	}

	f1 := ld.OnLossDetectionTimeout(d0)
	if !f1.PTOFired || f1.ProbePackets != 2 || ld.PTOCount != 1 {
		t.Fatalf("first PTO expiry = %+v count=%d", f1, ld.PTOCount)
	}
	d1, _ := ld.GetPtoTimeAndSpace()
	if !d1.Equal(t0.Add(1998 * time.Millisecond)) {
		t.Fatalf("backoff-1 PTO = %v, want %v", d1, t0.Add(1998*time.Millisecond))
	}

	f2 := ld.OnLossDetectionTimeout(d1)
	if !f2.PTOFired || ld.PTOCount != 2 {
		t.Fatalf("second PTO expiry count=%d", ld.PTOCount)
	}
	d2, _ := ld.GetPtoTimeAndSpace()
	if !d2.Equal(t0.Add(3996 * time.Millisecond)) {
		t.Fatalf("backoff-2 PTO = %v, want %v", d2, t0.Add(3996*time.Millisecond))
	}

	// Forward progress ACK resets pto_count; with nothing outstanding the PTO
	// disappears.
	ld.AckOne(rfc9002.Initial, 0, d2)
	if ld.PTOCount != 0 {
		t.Fatalf("pto_count after ACK = %d, want 0", ld.PTOCount)
	}
	if d, _ := ld.GetPtoTimeAndSpace(); !d.IsZero() {
		t.Fatalf("PTO still armed after acking sole packet: %v", d)
	}

	// AppData PTO includes max_ack_delay only after confirmation. Pin RTT.
	ld2 := rfc9002.NewLossDetector(rfc9002.WithMaxAckDelay(25 * time.Millisecond))
	ld2.SmoothedRTT = 100 * time.Millisecond
	ld2.RTTVar = 0
	ld2.HandshakeConfirmed = true
	ld2.SendAckEliciting(rfc9002.ApplicationData, 0, t0, mss)
	d, _ := ld2.GetPtoTimeAndSpace()
	if want := t0.Add(126 * time.Millisecond); !d.Equal(want) { // 100 + max(0,1) + 25
		t.Fatalf("AppData PTO = %v, want %v (includes max_ack_delay)", d, want)
	}

	// Initial space never adds max_ack_delay.
	ld3 := rfc9002.NewLossDetector(rfc9002.WithMaxAckDelay(25 * time.Millisecond))
	ld3.SmoothedRTT = 100 * time.Millisecond
	ld3.RTTVar = 0
	ld3.SendAckEliciting(rfc9002.Initial, 0, t0, mss)
	d3, _ := ld3.GetPtoTimeAndSpace()
	if want := t0.Add(101 * time.Millisecond); !d3.Equal(want) { // 100 + max(0,1), no ack delay
		t.Fatalf("Initial PTO = %v, want %v (no max_ack_delay)", d3, want)
	}
}

func TestLossDelayFormula(t *testing.T) {
	ld := rfc9002.NewLossDetector()
	ld.LatestRTT = 80 * time.Millisecond
	ld.SmoothedRTT = 100 * time.Millisecond
	want := time.Duration(9.0 / 8.0 * float64(100*time.Millisecond))
	if got := ld.LossDelay(); got != want {
		t.Fatalf("LossDelay = %v, want %v", got, want)
	}

	ld2 := rfc9002.NewLossDetector()
	// Explicitly zeroed estimates: the max(...) term is zero, so the
	// kGranularity floor wins.
	ld2.LatestRTT = 0
	ld2.SmoothedRTT = 0
	if got := ld2.LossDelay(); got != time.Millisecond {
		t.Fatalf("LossDelay zero-RTT = %v, want 1ms granularity", got)
	}
}

// Explicit QUIC ACK ranges selectively acknowledge blocks and drive the
// packet threshold within the same space.
func TestAckRangesSelective(t *testing.T) {
	ld := rfc9002.NewLossDetector()
	for n := rfc9002.PacketNumber(0); n < 10; n++ {
		ld.SendAckEliciting(rfc9002.ApplicationData, n, t0, mss)
	}
	out := ld.OnAckReceived(rfc9002.AckFrame{
		Space:        rfc9002.ApplicationData,
		LargestAcked: 7,
		Ranges:       []rfc9002.AckRange{rfc9002.Range(5, 7)},
	}, t0.Add(5*time.Millisecond))

	for _, n := range []rfc9002.PacketNumber{5, 6, 7} {
		if !containsNumber(out.NewlyAcked, n) {
			t.Fatalf("newly acked %v missing %d", out.NewlyAcked, n)
		}
	}
	// largest 7 - threshold 3 => packet 4 and below lost.
	if len(out.Lost) != 5 {
		t.Fatalf("lost = %v, want 0..4", lostNumbers(out))
	}
	for _, n := range []rfc9002.PacketNumber{0, 1, 2, 3, 4} {
		if !containsNumber(lostNumbers(out), n) {
			t.Fatalf("lost %v missing %d", lostNumbers(out), n)
		}
	}
	// 8 and 9 remain outstanding.
	if ld.InFlightForSpace(rfc9002.ApplicationData) != 2*mss {
		t.Fatalf("AppData in-flight = %d, want %d (pn8,pn9)",
			ld.InFlightForSpace(rfc9002.ApplicationData), 2*mss)
	}
}
