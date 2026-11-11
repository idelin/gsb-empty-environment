// Package jitterbuf implements an adaptive RTP jitter buffer for embedding in
// the receive path of a voice gateway.
//
// The buffer re-orders packets by RTP sequence number and RTP timestamp, both
// extended past their 16/32-bit wrap-around, estimates arrival jitter with the
// RFC 3550 A.8 smoothed estimator and derives an adaptive target playout
// delay. Delay is only changed at talkspurt boundaries (silence): within a
// talkspurt every frame is scheduled exactly one frame duration after the
// previous one. Packets arriving after their playout slot are dropped and
// counted, packet interruptions longer than the configured threshold flush
// the buffer and re-synchronize to the stream, and RTP timestamp
// discontinuities are treated as the start of a new talkspurt.
//
// Time comes from an injectable Clock: use WallClock in production and
// VirtualClock to drive deterministic arrival/playout sequences in tests.
package jitterbuf

import (
	"container/heap"
	"errors"
	"sync"
	"time"
)

// PopStatus describes the outcome of a JitterBuffer.Pop call.
type PopStatus int

const (
	// PopOK means a frame was available and is due for playout.
	PopOK PopStatus = iota
	// PopNotReady means the buffer holds no packet due at the current clock.
	PopNotReady
	// PopLate means the due playout slot has already passed; the consumer
	// should apply packet-loss concealment for one frame.
	PopLate
)

func (s PopStatus) String() string {
	switch s {
	case PopOK:
		return "ok"
	case PopNotReady:
		return "not-ready"
	case PopLate:
		return "late"
	default:
		return "unknown"
	}
}

// Packet is an RTP packet as observed on the receive path.
type Packet struct {
	// Seq is the raw 16-bit RTP sequence number.
	Seq uint16
	// Timestamp is the raw 32-bit RTP timestamp.
	Timestamp uint32
	// Marker marks the first packet of a talkspurt (RTP M bit).
	Marker bool
	// SID marks a silence-insertion-descriptor / comfort-noise frame.
	SID bool
	// Payload is copied into the returned frame; the caller may reuse it
	// after Push returns.
	Payload []byte
}

// Config configures a JitterBuffer. Zero-valued numeric fields fall back to
// DefaultConfig values.
type Config struct {
	// FrameDuration is the duration of one RTP frame (e.g. 20ms).
	FrameDuration time.Duration
	// ClockRate is the RTP timestamp clock rate (e.g. 8000 for G.711).
	ClockRate int
	// MinDelay / MaxDelay bound the adaptive target playout delay.
	MinDelay, MaxDelay time.Duration
	// DelayMargin is added on top of the estimated jitter when computing the
	// target playout delay.
	DelayMargin time.Duration
	// ResyncThreshold: an arrival gap longer than this flushes the buffer and
	// re-synchronizes to the next packet.
	ResyncThreshold time.Duration
	// JumpFrames: an RTP timestamp discontinuity of at least this many frame
	// durations (in either direction) starts a new talkspurt.
	JumpFrames int64
}

// DefaultConfig returns a conservative default configuration for 20ms frames
// on an 8kHz RTP clock.
func DefaultConfig() Config {
	return Config{
		FrameDuration:   20 * time.Millisecond,
		ClockRate:       8000,
		MinDelay:        40 * time.Millisecond,
		MaxDelay:        200 * time.Millisecond,
		DelayMargin:     20 * time.Millisecond,
		ResyncThreshold: 250 * time.Millisecond,
		JumpFrames:      3,
	}
}

// Stats exposes cumulative buffer counters.
type Stats struct {
	// Received is the number of packets accepted into the buffer.
	Received uint64
	// Duplicates is the number of packets dropped because their sequence
	// number was already buffered.
	Duplicates uint64
	// LateDropped is the number of packets that arrived after their playout
	// slot had already passed.
	LateDropped uint64
	// Flushed is the number of buffered packets discarded on re-sync.
	Flushed uint64
	// Played is the number of frames handed out through Pop.
	Played uint64
	// Lost is the number of playout slots that had to be skipped because the
	// matching packet was missing.
	Lost uint64
	// Resyncs is the number of long-gap re-synchronizations.
	Resyncs uint64
	// Talkspurts is the number of talkspurts observed.
	Talkspurts uint64
}

// PopResult is the result of a Pop call.
type PopResult struct {
	Status PopStatus
	// Packet is the played packet when Status == PopOK, otherwise the zero
	// value.
	Packet Packet
	// Scheduled is the virtual-clock time at which the frame was due.
	Scheduled time.Time
	// TargetDelay is the adaptive target delay in effect for this pop.
	TargetDelay time.Duration
	// FrameDelay is the packet's actual buffering delay (playout minus
	// arrival). Zero when no frame is played.
	FrameDelay time.Duration
	// Lost is true for PopLate and means the consumer should conceal one
	// frame.
	Lost bool
}

// JitterBuffer is an adaptive, single-stream RTP jitter buffer driven by an
// injectable Clock (use VirtualClock in tests, a wall clock in production).
//
// All methods are safe for concurrent use.
type JitterBuffer struct {
	mu sync.Mutex

	clock      Clock
	frame      time.Duration
	tsPerFrame int64
	minDelay   time.Duration
	maxDelay   time.Duration
	margin     time.Duration
	resyncGap  time.Duration
	jumpFrames int64

	// queue holds buffered packets ordered by extended sequence number.
	queue packetHeap
	// present records extended sequence numbers currently buffered, for
	// duplicate detection.
	present map[int64]struct{}

	// Reference points for extending 16/32-bit RTP values (RFC 3550).
	baseSeq     int64  // extended seq number of the first accepted packet
	maxSeq      int64  // highest raw-space sequence number seen (wrap detect)
	seqCycles   int64  // wrap cycles accumulated for sequence numbers
	maxRawTS    uint32 // highest raw timestamp seen on the current cycle
	tsWrapCount int64  // number of 32-bit timestamp wraps observed
	haveRefs    bool
	lastArrival time.Time
	haveArrival bool

	// Jitter estimator state (RFC 3550 A.8), expressed in time.Duration.
	jitter      time.Duration
	lastTransit time.Duration
	haveTransit bool
	targetDelay time.Duration

	// Playout state.
	playNextSeq   int64 // extended seq number of the next slot to play
	nextPlayAt    time.Time
	havePlayState bool
	playDelay     time.Duration // target delay applied to the active spurt

	// Talkspurt state.
	lastExtTS int64 // extended RTP timestamp of the previous accepted packet
	// Boundary spurt whose playout schedule must not overwrite the current
	// spurt's cursor until the current spurt has fully drained.
	pendingEntry *entry
	pendingAt    time.Time

	// Timeline anchor: extended RTP timestamp anchorTS corresponds to
	// virtual-clock time anchorAt.
	anchorTS int64
	anchorAt time.Time

	stats Stats
}

// New creates a JitterBuffer using cfg (zero fields defaulted) and clock.
func New(cfg Config, clock Clock) (*JitterBuffer, error) {
	if clock == nil {
		return nil, errors.New("jitterbuf: clock is required")
	}
	d := DefaultConfig()
	if cfg.FrameDuration <= 0 {
		cfg.FrameDuration = d.FrameDuration
	}
	if cfg.ClockRate <= 0 {
		cfg.ClockRate = d.ClockRate
	}
	if cfg.MinDelay <= 0 {
		cfg.MinDelay = d.MinDelay
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = d.MaxDelay
	}
	if cfg.DelayMargin < 0 {
		cfg.DelayMargin = d.DelayMargin
	}
	if cfg.ResyncThreshold <= 0 {
		cfg.ResyncThreshold = d.ResyncThreshold
	}
	if cfg.JumpFrames <= 0 {
		cfg.JumpFrames = d.JumpFrames
	}
	if cfg.MaxDelay < cfg.MinDelay {
		return nil, errors.New("jitterbuf: MaxDelay must be >= MinDelay")
	}

	jb := &JitterBuffer{
		clock:       clock,
		frame:       cfg.FrameDuration,
		tsPerFrame:  int64(cfg.ClockRate) * int64(cfg.FrameDuration) / int64(time.Second),
		minDelay:    cfg.MinDelay,
		maxDelay:    cfg.MaxDelay,
		margin:      cfg.DelayMargin,
		resyncGap:   cfg.ResyncThreshold,
		jumpFrames:  cfg.JumpFrames,
		present:     make(map[int64]struct{}),
		targetDelay: cfg.MinDelay,
	}
	if jb.tsPerFrame <= 0 {
		return nil, errors.New("jitterbuf: ClockRate/FrameDuration produce zero timestamp units per frame")
	}
	heap.Init(&jb.queue)
	return jb, nil
}

// Push inserts a packet observed at the current clock time.
func (jb *JitterBuffer) Push(p Packet) {
	jb.mu.Lock()
	defer jb.mu.Unlock()

	now := jb.clock.Now()

	// Long interruption: drop everything and re-synchronize.
	if jb.haveArrival && now.Sub(jb.lastArrival) > jb.resyncGap {
		jb.resyncLocked()
	}
	jb.lastArrival = now
	jb.haveArrival = true

	if !jb.haveRefs {
		jb.initFirstLocked(p, now)
		return
	}

	extSeq := jb.extendSeqLocked(p.Seq)
	extTS := jb.extendTsLocked(p.Timestamp)

	// Re-sequence: duplicate of something already buffered.
	if _, ok := jb.present[extSeq]; ok {
		jb.stats.Duplicates++
		return
	}

	// Too late: the playout slot for this sequence number has passed.
	if jb.havePlayState && extSeq < jb.playNextSeq {
		jb.stats.LateDropped++
		return
	}

	newSpurt := jb.classifySpurtLocked(p, extTS)
	jb.updateJitterLocked(extTS, now, newSpurt)

	entry := &entry{
		pkt:     p,
		extSeq:  extSeq,
		extTS:   extTS,
		arrival: now,
		sid:     p.SID,
	}
	if payload := p.Payload; len(payload) > 0 {
		entry.pkt.Payload = append([]byte(nil), payload...)
	}
	heap.Push(&jb.queue, entry)
	jb.present[extSeq] = struct{}{}
	jb.stats.Received++

	if newSpurt {
		jb.startSpurtLocked(entry)
	} else {
		jb.lastExtTS = extTS
	}
}

// Pop returns the frame due at the current clock time.
func (jb *JitterBuffer) Pop() PopResult {
	jb.mu.Lock()
	defer jb.mu.Unlock()

	jb.activatePendingLocked()
	if jb.queue.Len() == 0 || !jb.havePlayState {
		return PopResult{Status: PopNotReady, TargetDelay: jb.targetDelay}
	}

	now := jb.clock.Now()
	if now.Before(jb.nextPlayAt) {
		return PopResult{Status: PopNotReady, TargetDelay: jb.targetDelay}
	}

	head := jb.queue[0]
	if head.extSeq > jb.playNextSeq {
		// The due slot is missing (or still somewhere in the network). Report
		// one lost frame and advance the playout cursor by exactly one frame.
		jb.stats.Lost++
		scheduled := jb.nextPlayAt
		jb.playNextSeq++
		jb.nextPlayAt = jb.nextPlayAt.Add(jb.frame)
		return PopResult{
			Status:      PopLate,
			Scheduled:   scheduled,
			TargetDelay: jb.playDelay,
			Lost:        true,
		}
	}

	// head.extSeq == playNextSeq (smaller values are discarded in Push).
	popped := heap.Pop(&jb.queue).(*entry)
	delete(jb.present, popped.extSeq)

	scheduled := jb.nextPlayAt
	frameDelay := scheduled.Sub(popped.arrival)
	jb.playNextSeq++
	jb.nextPlayAt = scheduled.Add(jb.frame)
	jb.stats.Played++

	return PopResult{
		Status:      PopOK,
		Packet:      popped.pkt,
		Scheduled:   scheduled,
		TargetDelay: jb.playDelay,
		FrameDelay:  frameDelay,
	}
}

// activatePendingLocked switches the playout cursor to a boundary spurt once
// every frame of the spurt currently being played has been consumed.
func (jb *JitterBuffer) activatePendingLocked() {
	if jb.pendingEntry == nil {
		return
	}
	// Wait until the old spurt's cursor has reached the boundary packet.
	if jb.playNextSeq < jb.pendingEntry.extSeq {
		return
	}
	jb.playNextSeq = jb.pendingEntry.extSeq
	jb.nextPlayAt = jb.pendingAt
	jb.havePlayState = true
	jb.playDelay = jb.targetDelay
	jb.pendingEntry = nil
}

// Buffered returns the number of packets currently queued.
func (jb *JitterBuffer) Buffered() int {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	return jb.queue.Len()
}

// TargetDelay returns the current adaptive target playout delay.
func (jb *JitterBuffer) TargetDelay() time.Duration {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	return jb.targetDelay
}

// Jitter returns the current smoothed arrival-jitter estimate.
func (jb *JitterBuffer) Jitter() time.Duration {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	return jb.jitter
}

// Stats returns a copy of the cumulative counters.
func (jb *JitterBuffer) Stats() Stats {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	return jb.stats
}

// initFirstLocked absorbs the first packet and seeds every reference point.
func (jb *JitterBuffer) initFirstLocked(p Packet, now time.Time) {
	jb.baseSeq = int64(p.Seq)
	jb.maxSeq = int64(p.Seq)
	jb.maxRawTS = p.Timestamp
	jb.haveRefs = true

	extSeq := jb.baseSeq
	extTS := jb.extendTsLocked(p.Timestamp)

	// Anchor the RTP timeline at the first packet's arrival.
	jb.anchorTS = extTS
	jb.anchorAt = now

	entry := jb.newEntryLocked(p, extSeq, extTS, now)
	heap.Push(&jb.queue, entry)
	jb.present[extSeq] = struct{}{}
	jb.stats.Received++

	jb.lastExtTS = extTS
	jb.stats.Talkspurts++

	// Playout reference: base RTP timestamp maps onto the first arrival time.
	baseAt := jb.rtpSendTimeLocked(extTS)
	jb.playNextSeq = extSeq
	jb.nextPlayAt = baseAt.Add(jb.targetDelay)
	jb.havePlayState = true
	jb.playDelay = jb.targetDelay

	jb.lastTransit = now.Sub(jb.rtpSendTimeLocked(extTS))
	jb.haveTransit = true
}

// resyncLocked discards all buffered state after a long packet interruption
// so the next accepted packet seeds a fresh playout timeline.
func (jb *JitterBuffer) resyncLocked() {
	jb.stats.Flushed += uint64(jb.queue.Len())
	jb.stats.Resyncs++

	jb.queue = nil
	jb.present = make(map[int64]struct{})
	jb.pendingEntry = nil
	jb.haveRefs = false
	jb.havePlayState = false
	jb.haveTransit = false
	jb.jitter = 0
	jb.maxRawTS = 0
	jb.tsWrapCount = 0
	jb.seqCycles = 0
	jb.maxSeq = 0

}

// classifySpurtLocked decides whether p starts a new talkspurt. A marker bit,
// an SID frame, or an RTP timestamp discontinuity (jump in either direction)
// relative to the previous accepted packet all end the current talkspurt.
func (jb *JitterBuffer) classifySpurtLocked(p Packet, extTS int64) bool {
	deltaTS := extTS - jb.lastExtTS
	jumpLimit := jb.jumpFrames * jb.tsPerFrame

	switch {
	case p.Marker:
		return true
	case p.SID:
		return true
	case deltaTS > jumpLimit || deltaTS < -jumpLimit:
		return true
	default:
		return false
	}
}

// startSpurtLocked applies the freshly computed target delay at a talkspurt
// boundary and schedules the first frame of the new talkspurt. Delay never
// changes while a talkspurt is in progress.
func (jb *JitterBuffer) startSpurtLocked(e *entry) {
	// classifySpurtLocked only calls here at a boundary, so every call is a
	// new talkspurt even if the previous one was never explicitly closed.
	jb.stats.Talkspurts++

	// Boundary spurts are scheduled from the packet's own arrival, not the
	// (now discontinuous) RTP timestamp timeline.
	playAt := e.arrival.Add(jb.targetDelay)
	if now := jb.clock.Now(); playAt.Before(now) {
		playAt = now
	}

	// If the previous spurt still owns the playout cursor, do not disturb it:
	// queue the boundary schedule and switch over once the old spurt drains.
	if jb.havePlayState && e.extSeq > jb.playNextSeq {
		jb.pendingEntry = e
		jb.pendingAt = playAt
	} else {
		jb.playNextSeq = e.extSeq
		jb.nextPlayAt = playAt
		jb.havePlayState = true
		jb.playDelay = jb.targetDelay
	}

	// Re-seed the transit reference at the boundary so the talkspurt gap
	// itself is not interpreted as massive network transit variation.
	jb.lastTransit = jb.targetDelay
	jb.haveTransit = true
	jb.lastExtTS = e.extTS
}

// updateJitterLocked runs the RFC 3550 A.8 smoothed-jitter estimator and
// recomputes the target delay. The estimate is updated for every in-order
// packet; the returned target is only applied at talkspurt boundaries.
func (jb *JitterBuffer) updateJitterLocked(extTS int64, now time.Time, newSpurt bool) {
	if newSpurt {
		// startSpurtLocked re-seeds the transit reference; nothing to measure.
		return
	}

	transit := now.Sub(jb.rtpSendTimeLocked(extTS))
	if !jb.haveTransit {
		jb.lastTransit = transit
		jb.haveTransit = true
		return
	}

	d := transit - jb.lastTransit
	if d < 0 {
		d = -d
	}
	// J += (|D| - J) / 16, in integer arithmetic.
	jb.jitter += (d - jb.jitter) / 16
	jb.lastTransit = transit

	target := jb.minDelay
	if want := jb.jitter + jb.margin; want > target {
		target = want
	}
	if target > jb.maxDelay {
		target = jb.maxDelay
	}
	jb.targetDelay = target
}

// extendSeqLocked extends a raw 16-bit sequence number to 64 bits (RFC 3550).
func (jb *JitterBuffer) extendSeqLocked(seq uint16) int64 {
	const mod = 1 << 16
	high := uint16(jb.maxSeq)
	forward := int64(seq) - int64(high)
	switch {
	case forward > 0 && forward < mod/2:
		// In-order forward step, no wrap.
	case forward <= 0 && forward > -mod/2:
		// Out-of-order packet within the current cycle.
		return jb.seqCycles*mod + int64(seq)
	case forward <= -mod/2:
		// Forward wrap (e.g. 65534 -> 0): advance one cycle.
		jb.seqCycles++
	default:
		// forward >= mod/2: out-of-order packet from the previous cycle
		// (watermark already in the new cycle).
		return jb.seqCycles*mod + int64(seq) - mod
	}
	jb.maxSeq = int64(seq)
	return jb.seqCycles*mod + int64(seq)
}

// extendTsLocked extends a raw 32-bit RTP timestamp to a signed 64-bit value.
func (jb *JitterBuffer) extendTsLocked(ts uint32) int64 {
	const mod = int64(1) << 32
	high := jb.maxRawTS
	forward := int64(ts) - int64(high)
	switch {
	case forward > 0 && forward < mod/2:
		// In-order forward step, no wrap.
		jb.maxRawTS = ts
	case forward <= 0 && forward > -mod/2:
		// Out-of-order packet within the current cycle.
		return jb.tsWrapCount*mod + int64(ts)
	case forward <= -mod/2:
		// Forward wrap across the 32-bit boundary.
		jb.tsWrapCount++
		jb.maxRawTS = ts
	default:
		// Out-of-order packet from the previous 32-bit cycle.
		return jb.tsWrapCount*mod + int64(ts) - mod
	}
	return jb.tsWrapCount*mod + int64(ts)
}

// rtpSendTimeLocked maps an extended RTP timestamp to the sender-side
// position on the virtual clock (first packet's RTP time aligned to its
// arrival, i.e. zero assumed transit at the anchor). It is used purely for
// transit-time/jitter estimation.
func (jb *JitterBuffer) rtpSendTimeLocked(extTS int64) time.Time {
	return jb.anchorAt.Add(time.Duration((extTS - jb.anchorTS) * int64(jb.frame) / jb.tsPerFrame))
}

func (jb *JitterBuffer) newEntryLocked(p Packet, extSeq, extTS int64, now time.Time) *entry {
	e := &entry{
		pkt:     p,
		extSeq:  extSeq,
		extTS:   extTS,
		arrival: now,
		sid:     p.SID,
	}
	if payload := p.Payload; len(payload) > 0 {
		e.pkt.Payload = append([]byte(nil), payload...)
	}
	return e
}

// entry is a buffered packet plus its extended ordering keys.
type entry struct {
	pkt     Packet
	extSeq  int64
	extTS   int64
	arrival time.Time
	sid     bool
}

// packetHeap orders entries by extended sequence number (and therefore, for
// well-formed streams, by RTP timestamp).
type packetHeap []*entry

func (h packetHeap) Len() int { return len(h) }

func (h packetHeap) Less(i, j int) bool {
	if h[i].extSeq != h[j].extSeq {
		return h[i].extSeq < h[j].extSeq
	}
	return h[i].extTS < h[j].extTS
}

func (h packetHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *packetHeap) Push(x any) {
	e := x.(*entry)
	*h = append(*h, e)
}

func (h *packetHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return e
}
