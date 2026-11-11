package jitterbuf

import (
	"testing"
	"time"
)

const (
	testFrame = 20 * time.Millisecond
	testRate  = 8000
	testTS    = int64(160) // RTP timestamp units per 20ms frame at 8kHz
)

func testConfig() Config {
	cfg := DefaultConfig()
	cfg.FrameDuration = testFrame
	cfg.ClockRate = testRate
	cfg.MinDelay = 40 * time.Millisecond
	cfg.MaxDelay = 200 * time.Millisecond
	cfg.DelayMargin = 20 * time.Millisecond
	cfg.ResyncThreshold = 250 * time.Millisecond
	cfg.JumpFrames = 3
	return cfg
}

func newTestJB(t *testing.T) (*JitterBuffer, *VirtualClock) {
	t.Helper()
	clock := NewVirtualClock(time.Unix(0, 0))
	jb, err := New(testConfig(), clock)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return jb, clock
}

func mkPkt(seq uint16, ts uint32) Packet {
	return Packet{Seq: seq, Timestamp: ts, Payload: []byte{byte(seq), byte(ts >> 8), byte(ts)}}
}

func mustPopOK(t *testing.T, jb *JitterBuffer, wantSeq uint16) PopResult {
	t.Helper()
	res := jb.Pop()
	if res.Status != PopOK {
		t.Fatalf("Pop status = %s, want ok (seq %d)", res.Status, wantSeq)
	}
	if res.Packet.Seq != wantSeq {
		t.Fatalf("Pop seq = %d, want %d", res.Packet.Seq, wantSeq)
	}
	return res
}

func mustPopStatus(t *testing.T, jb *JitterBuffer, want PopStatus) PopResult {
	t.Helper()
	res := jb.Pop()
	if res.Status != want {
		t.Fatalf("Pop status = %s, want %s", res.Status, want)
	}
	return res
}

// TestInOrderPlayout verifies the basic timeline: the first frame is delayed
// by MinDelay and subsequent frames follow exactly one frame duration apart.
func TestInOrderPlayout(t *testing.T) {
	jb, clock := newTestJB(t)

	const n = 10
	start := clock.Now()
	for i := 0; i < n; i++ {
		jb.Push(mkPkt(uint16(i), uint32(int64(i)*testTS)))
		clock.Advance(testFrame)
	}

	// Nothing is due before the first scheduled slot.
	clock.Set(start.Add(39 * time.Millisecond))
	mustPopStatus(t, jb, PopNotReady)

	clock.Set(start.Add(40 * time.Millisecond))
	first := mustPopOK(t, jb, 0)
	if !first.Scheduled.Equal(start.Add(40 * time.Millisecond)) {
		t.Fatalf("first scheduled = %v, want %v", first.Scheduled, start.Add(40*time.Millisecond))
	}
	if first.TargetDelay != 40*time.Millisecond {
		t.Fatalf("initial target delay = %v, want 40ms", first.TargetDelay)
	}

	prevSched := first.Scheduled
	for i := 1; i < n; i++ {
		clock.Advance(testFrame)
		res := mustPopOK(t, jb, uint16(i))
		if !res.Scheduled.Equal(prevSched.Add(testFrame)) {
			t.Fatalf("frame %d scheduled = %v, want %v", i, res.Scheduled, prevSched.Add(testFrame))
		}
		prevSched = res.Scheduled
	}

	stats := jb.Stats()
	if stats.Played != n || stats.Lost != 0 || stats.LateDropped != 0 || stats.Duplicates != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

// TestReorderAndWraparound verifies out-of-order re-sequencing across a 16-bit
// sequence-number wrap and a 32-bit timestamp wrap.
func TestReorderAndWraparound(t *testing.T) {
	jb, clock := newTestJB(t)
	start := clock.Now()

	// Packets straddling both the 16-bit seq wrap and 32-bit timestamp wrap.
	seqs := []uint16{65534, 65535, 0, 1, 2, 3}
	tss := []uint32{0xFFFFFF60, 0xFFFFFFC0, 0x20, 0xC0, 0x160, 0x200}

	// Push them shuffled, starting naturally with the first sender packet.
	order := []int{0, 2, 1, 3, 5, 4} // 65534,0,65535,1,3,2
	for _, idx := range order {
		jb.Push(mkPkt(seqs[idx], tss[idx]))
		clock.Advance(5 * time.Millisecond)
	}

	clock.Set(start.Add(40 * time.Millisecond))
	for i, seq := range seqs {
		res := mustPopOK(t, jb, seq)
		wantSched := start.Add(40 * time.Millisecond).Add(time.Duration(i) * testFrame)
		if !res.Scheduled.Equal(wantSched) {
			t.Fatalf("seq %d scheduled = %v, want %v", seq, res.Scheduled, wantSched)
		}
		clock.Advance(testFrame)
	}
}

// TestDuplicates verifies retransmitted copies are counted and ignored.
func TestDuplicates(t *testing.T) {
	jb, clock := newTestJB(t)

	jb.Push(mkPkt(0, 0))
	clock.Advance(testFrame)
	jb.Push(mkPkt(1, uint32(testTS)))
	clock.Advance(5 * time.Millisecond)
	jb.Push(mkPkt(1, uint32(testTS))) // duplicate
	jb.Push(mkPkt(0, 0))              // duplicate of an already queued seq

	if got := jb.Stats().Duplicates; got != 2 {
		t.Fatalf("Duplicates = %d, want 2", got)
	}
	if jb.Buffered() != 2 {
		t.Fatalf("Buffered = %d, want 2", jb.Buffered())
	}
}

// TestLateDrop verifies packets arriving after their playout slot are dropped
// and counted, while playout keeps moving one frame per tick.
func TestLateDrop(t *testing.T) {
	jb, clock := newTestJB(t)
	start := clock.Now()

	jb.Push(mkPkt(0, 0))
	clock.Advance(testFrame)
	jb.Push(mkPkt(1, uint32(testTS)))
	clock.Advance(testFrame)
	jb.Push(mkPkt(2, 2*uint32(testTS)))

	// Drain the first two frames.
	clock.Set(start.Add(40 * time.Millisecond))
	mustPopOK(t, jb, 0)
	clock.Advance(testFrame)
	mustPopOK(t, jb, 1)

	// Seq 0 and 1 are now behind the play point.
	jb.Push(mkPkt(0, 0))
	jb.Push(mkPkt(1, uint32(testTS)))
	if got := jb.Stats().LateDropped; got != 2 {
		t.Fatalf("LateDropped = %d, want 2", got)
	}

	clock.Advance(testFrame)
	mustPopOK(t, jb, 2)
	if got := jb.Stats().Played; got != 3 {
		t.Fatalf("Played = %d, want 3", got)
	}
}

// TestMissingFrame verifies a gap in the sequence produces one PopLate and
// the following in-order frame still plays at its scheduled slot.
func TestMissingFrame(t *testing.T) {
	jb, clock := newTestJB(t)
	start := clock.Now()

	jb.Push(mkPkt(0, 0))
	clock.Advance(testFrame)
	// seq 1 lost
	jb.Push(mkPkt(2, 2*uint32(testTS)))

	clock.Set(start.Add(40 * time.Millisecond))
	mustPopOK(t, jb, 0)
	clock.Advance(testFrame)

	late := mustPopStatus(t, jb, PopLate)
	if !late.Lost || !late.Scheduled.Equal(start.Add(60*time.Millisecond)) {
		t.Fatalf("unexpected late result: %+v", late)
	}
	mustPopStatus(t, jb, PopNotReady) // a second Pop in the same tick waits

	clock.Advance(testFrame)
	frame2 := mustPopOK(t, jb, 2)
	if !frame2.Scheduled.Equal(start.Add(80 * time.Millisecond)) {
		t.Fatalf("frame 2 scheduled = %v, want 80ms", frame2.Scheduled)
	}

	stats := jb.Stats()
	if stats.Lost != 1 || stats.Played != 2 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

// TestAdaptiveDelayOnlyAtSpurtBoundary is the core adaptation check: jitter
// grows the target delay during a talkspurt, but playout spacing stays fixed;
// the new target only takes effect on the next talkspurt (after silence), and
// decays again once the network becomes regular.
func TestAdaptiveDelayOnlyAtSpurtBoundary(t *testing.T) {
	// Low floor so a measured ~15ms jitter actually moves the target above it.
	cfg := testConfig()
	cfg.MinDelay = 20 * time.Millisecond
	clock := NewVirtualClock(time.Unix(0, 0))
	jb, err := New(cfg, clock)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	start := clock.Now()

	// Spurt A: 24 frames with alternating +/-5ms spacing around the nominal
	// 20ms sender spacing. The smoothed jitter converges upward.
	const nA = 24
	for i := 0; i < nA; i++ {
		arrival := start.Add(time.Duration(i) * testFrame)
		if i%2 == 1 {
			arrival = arrival.Add(10 * time.Millisecond)
		} else if i > 0 {
			arrival = arrival.Add(-10 * time.Millisecond)
		}
		clock.Set(arrival)
		jb.Push(mkPkt(uint16(i), uint32(int64(i)*testTS)))
	}

	if jb.Jitter() < 12*time.Millisecond {
		t.Fatalf("jitter after spurt A = %v, want >= 12ms", jb.Jitter())
	}
	rawTargetA := jb.Jitter() + 20*time.Millisecond
	wantTargetA := rawTargetA
	if wantTargetA < 20*time.Millisecond {
		wantTargetA = 20 * time.Millisecond // MinDelay clamp
	}
	if jb.TargetDelay() != wantTargetA {
		t.Fatalf("target after spurt A = %v, want %v", jb.TargetDelay(), wantTargetA)
	}
	if wantTargetA <= 20*time.Millisecond {
		t.Fatalf("test setup failed: target never grew above floor: %v", wantTargetA)
	}

	// Within spurt A, every frame still plays exactly one frame apart: the
	// increased target does not move the in-progress talkspurt.
	clock.Set(start.Add(20 * time.Millisecond))
	prev := mustPopOK(t, jb, 0).Scheduled
	for i := 1; i < nA; i++ {
		clock.Advance(testFrame)
		res := mustPopOK(t, jb, uint16(i))
		if !res.Scheduled.Equal(prev.Add(testFrame)) {
			t.Fatalf("spurt A frame %d spacing changed: %v vs %v", i, res.Scheduled, prev.Add(testFrame))
		}
		prev = res.Scheduled
	}

	// Silence, then a new talkspurt B marked with the M bit. Its first frame
	// must honor the larger, adapted target delay.
	spurtBBase := start.Add(time.Second)
	clock.Set(spurtBBase)
	jb.Push(Packet{Seq: 100, Timestamp: 10000, Marker: true, Payload: []byte{1}})

	gotBBase := spurtBBase.Add(jb.TargetDelay())
	clock.Set(gotBBase)
	res := jb.Pop()
	if res.Status != PopOK || res.Packet.Seq != 100 {
		t.Fatalf("spurt B first pop = %+v", res)
	}
	if !res.Scheduled.Equal(gotBBase) {
		t.Fatalf("spurt B base delay = %v, want %v (target %v)", res.Scheduled, gotBBase, jb.TargetDelay())
	}
	if res.TargetDelay <= 20*time.Millisecond {
		t.Fatalf("spurt B adapted target = %v, want growth above 20ms floor", res.TargetDelay)
	}

	// Regular arrivals in spurt B decay the jitter/target toward MinDelay.
	const nB = 40
	jbJitterBeforeB := jb.Jitter()
	for i := 1; i < nB; i++ {
		clock.Set(spurtBBase.Add(time.Duration(i) * testFrame))
		jb.Push(mkPkt(uint16(100+i), uint32(10000+int64(i)*testTS)))
	}
	if jb.Jitter() > jbJitterBeforeB {
		t.Fatalf("jitter grew during a regular spurt: %v > %v", jb.Jitter(), jbJitterBeforeB)
	}

	// Drain B; in-spurt spacing remains exactly one frame regardless.
	clock.Set(gotBBase.Add(testFrame))
	prev = gotBBase
	for i := 1; i < nB; i++ {
		res := mustPopOK(t, jb, uint16(100+i))
		if !res.Scheduled.Equal(prev.Add(testFrame)) {
			t.Fatalf("spurt B frame %d spacing changed", i)
		}
		prev = res.Scheduled
		clock.Set(prev.Add(testFrame))
	}

	// After another quiet period, spurt C should start near MinDelay.
	spurtCBase := spurtBBase.Add(3 * time.Second)
	clock.Set(spurtCBase)
	jb.Push(Packet{Seq: 500, Timestamp: 40000, Marker: true, Payload: []byte{2}})
	clock.Set(spurtCBase.Add(jb.TargetDelay()))
	resC := mustPopOK(t, jb, 500)
	if resC.TargetDelay > 25*time.Millisecond {
		t.Fatalf("spurt C target = %v, want decay close to MinDelay", resC.TargetDelay)
	}

	stats := jb.Stats()
	if stats.Talkspurts != 3 {
		t.Fatalf("Talkspurts = %d, want 3", stats.Talkspurts)
	}
	if stats.Lost != 0 || stats.LateDropped != 0 {
		t.Fatalf("unexpected loss stats: %+v", stats)
	}
}

// TestResyncAfterLongGap verifies an interruption longer than the resync
// threshold flushes buffered packets and re-synchronizes playout to the next
// packet's arrival.
func TestResyncAfterLongGap(t *testing.T) {
	jb, clock := newTestJB(t)
	start := clock.Now()

	jb.Push(mkPkt(0, 0))
	clock.Advance(testFrame)
	jb.Push(mkPkt(1, uint32(testTS)))

	// Gap longer than ResyncThreshold (250ms); a stale packet is still queued.
	clock.Set(start.Add(400 * time.Millisecond))
	jb.Push(mkPkt(20, uint32(20*testTS)))

	stats := jb.Stats()
	if stats.Resyncs != 1 || stats.Flushed != 2 {
		t.Fatalf("resync stats = %+v, want 1 resync / 2 flushed", stats)
	}

	// The new stream seeds a fresh timeline anchored at its own arrival.
	resyncAt := clock.Now()
	clock.Set(resyncAt.Add(40 * time.Millisecond))
	res := mustPopOK(t, jb, 20)
	if !res.Scheduled.Equal(resyncAt.Add(40 * time.Millisecond)) {
		t.Fatalf("resync scheduled = %v, want %v", res.Scheduled, resyncAt.Add(40*time.Millisecond))
	}

	// Late packets from before the resync are irrelevant; playout continues.
	clock.Advance(testFrame)
	jb.Push(mkPkt(21, 21*uint32(testTS)))
	mustPopOK(t, jb, 21)
	if got := jb.Stats().Resyncs; got != 1 {
		t.Fatalf("Resyncs = %d, want 1", got)
	}
}

// TestTimestampJumpNewSpurt verifies a timestamp discontinuity without the M
// bit is still treated as a new talkspurt and scheduled at its own boundary.
func TestTimestampJumpNewSpurt(t *testing.T) {
	jb, clock := newTestJB(t)
	start := clock.Now()

	jb.Push(mkPkt(0, 0))
	clock.Advance(testFrame)
	jb.Push(mkPkt(1, uint32(testTS)))

	// Forward jump of 10 frames (> JumpFrames=3), no marker bit.
	jumpAt := start.Add(40 * time.Millisecond)
	clock.Set(jumpAt)
	jb.Push(mkPkt(2, uint32(12*testTS)))

	if jb.Stats().Talkspurts != 2 {
		t.Fatalf("Talkspurts = %d, want 2", jb.Stats().Talkspurts)
	}

	// Old spurt drains first.
	clock.Set(start.Add(40 * time.Millisecond))
	mustPopOK(t, jb, 0)
	clock.Advance(testFrame)
	mustPopOK(t, jb, 1)

	// Jumped packet is scheduled relative to its own arrival plus target.
	clock.Set(jumpAt.Add(jb.TargetDelay()))
	res := mustPopOK(t, jb, 2)
	if !res.Scheduled.Equal(jumpAt.Add(jb.TargetDelay())) {
		t.Fatalf("jump frame scheduled = %v, want arrival+target", res.Scheduled)
	}
}

// TestEmptyBufferNotReady verifies Pop on an empty buffer reports NotReady
// rather than fabricating a frame.
func TestEmptyBufferNotReady(t *testing.T) {
	jb, _ := newTestJB(t)
	mustPopStatus(t, jb, PopNotReady)
	if jb.Buffered() != 0 {
		t.Fatalf("Buffered = %d, want 0", jb.Buffered())
	}
}

// TestBackwardTimestampJump verifies a backwards RTP timestamp jump is also
// treated as a new talkspurt boundary.
func TestBackwardTimestampJump(t *testing.T) {
	jb, clock := newTestJB(t)

	jb.Push(mkPkt(0, 100*uint32(testTS)))
	clock.Advance(testFrame)
	jb.Push(mkPkt(1, 101*uint32(testTS)))
	clock.Advance(testFrame)
	// Backwards jump of 50 frames, no marker.
	jb.Push(mkPkt(2, 50*uint32(testTS)))

	if jb.Stats().Talkspurts != 2 {
		t.Fatalf("Talkspurts = %d, want 2", jb.Stats().Talkspurts)
	}
	// All three packets remain buffered and come out in seq order.
	if jb.Buffered() != 3 {
		t.Fatalf("Buffered = %d, want 3", jb.Buffered())
	}
}

// TestDelayOnlyChangesAtBoundary proves target delay stays constant inside a
// talkspurt even while jitter (and the computed target) is moving.
func TestDelayOnlyChangesAtBoundary(t *testing.T) {
	cfg := testConfig()
	cfg.MinDelay = 20 * time.Millisecond
	clock := NewVirtualClock(time.Unix(0, 0))
	jb, err := New(cfg, clock)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	start := clock.Now()

	// First frame anchors the spurt.
	jb.Push(mkPkt(0, 0))
	firstTarget := jb.TargetDelay()

	for i := 1; i < 12; i++ {
		arrival := start.Add(time.Duration(i) * testFrame)
		if i%2 == 1 {
			arrival = arrival.Add(8 * time.Millisecond)
		} else {
			arrival = arrival.Add(-8 * time.Millisecond)
		}
		clock.Set(arrival)
		jb.Push(mkPkt(uint16(i), uint32(int64(i)*testTS)))
	}
	if jb.Jitter() == 0 {
		t.Fatal("expected non-zero jitter after jittery arrivals")
	}

	// Drain the whole spurt; every pop reports the same, original target.
	clock.Set(start.Add(firstTarget))
	for i := 0; i < 12; i++ {
		res := mustPopOK(t, jb, uint16(i))
		if res.TargetDelay != firstTarget {
			t.Fatalf("frame %d target changed in-spurt: %v vs %v", i, res.TargetDelay, firstTarget)
		}
		clock.Advance(testFrame)
	}
}

// TestConfigValidation covers constructor defaults and invalid configuration.
func TestConfigValidation(t *testing.T) {
	clock := NewVirtualClock(time.Unix(0, 0))
	if _, err := New(DefaultConfig(), nil); err == nil {
		t.Fatal("nil clock should fail")
	}

	cfg := DefaultConfig()
	cfg.MaxDelay = cfg.MinDelay - time.Millisecond
	if _, err := New(cfg, clock); err == nil {
		t.Fatal("MaxDelay < MinDelay should fail")
	}

	jb, err := New(Config{}, clock) // everything defaulted
	if err != nil {
		t.Fatalf("zero config: %v", err)
	}
	if jb.frame != 20*time.Millisecond || jb.tsPerFrame != 160 || jb.targetDelay != 40*time.Millisecond {
		t.Fatalf("unexpected defaults: frame=%v tsPerFrame=%d target=%v", jb.frame, jb.tsPerFrame, jb.targetDelay)
	}
}

// TestConcurrentAccess is a smoke test that the mutex keeps Push/Pop/Stats
// consistent under concurrent access.
func TestConcurrentAccess(t *testing.T) {
	jb, clock := newTestJB(t)
	done := make(chan struct{})

	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			clock.Advance(time.Millisecond)
			jb.Push(mkPkt(uint16(i), uint32(int64(i)*testTS)))
		}
	}()
	for i := 0; i < 500; i++ {
		_ = jb.Pop()
		_ = jb.Stats()
		_ = jb.Buffered()
	}
	<-done
}
