package netsec

import (
	"errors"
	"fmt"
)

var (
	// ErrQueueIsolated is returned by every operation once the queue has been
	// isolated after an ambiguous completion. The only recovery is Reset.
	ErrQueueIsolated = errors.New("netsec: queue isolated after an ambiguous completion")
	// ErrQueueFull is returned by Submit when the target slot is still in flight.
	// It is also returned when the in-flight sequence span already covers half
	// of the tag space; the oldest request must complete before more submissions
	// can be recovered unambiguously.
	ErrQueueFull = errors.New("netsec: completion ring has no free slot")
	// ErrSeqExhausted is returned by Submit when the per-epoch submission
	// sequence space is used up. Drain the queue, then call SwitchEpoch.
	ErrSeqExhausted = errors.New("netsec: submission sequence space exhausted; drain the queue and switch epoch")
	// ErrNotDrained is returned by SwitchEpoch while requests are in flight.
	ErrNotDrained = errors.New("netsec: queue still has in-flight requests")
)

// Tenant identifies the owner of a leased buffer.
type Tenant uint32

// Lease identifies one leased buffer.
type Lease uint64

// ReleaseFunc is invoked exactly once per submitted lease: either when the
// device validly completes the request, or when Reset retires it.
type ReleaseFunc func(tenant Tenant, lease Lease)

// Config configures a Ring.
//
// Safety contract: the truncated tag space (1<<TruncBits) must be at least
// twice the ring size. Then at most half of the tag space is ever in flight,
// so any tag the hardware returns maps to at most one full sequence number
// inside the recovery window, and a completion delayed by one full ring
// cycle can never alias the slot's new occupant. Submit enforces the
// half-tag-space span with ErrQueueFull backpressure.
type Config struct {
	Slots     int  // ring slots, power of two >= 2
	TruncBits uint // tag bits the hardware returns per completion
	SeqBits   uint // submission sequence bits per epoch, > TruncBits
	OnRelease ReleaseFunc
}

// Stats counts ring events since construction (cumulative across epochs).
type Stats struct {
	Submitted     uint64
	Completed     uint64
	Released      uint64
	Stale         uint64 // duplicate / late / post-reset completions, ignored
	Isolations    uint64
	Resets        uint64
	EpochSwitches uint64
}

type slotState struct {
	inFlight bool
	seq      uint64 // full submission sequence within the current epoch
	tenant   Tenant
	lease    Lease
}

// Ring tracks one DMA completion queue for a Go driver host.
//
// A queue slot is bound to (submission sequence, tenant, buffer lease) at
// Submit time. The hardware reports completions as (slot, tag) where tag is
// the low TruncBits of the sequence. Complete reconstructs the full sequence
// under the at-most-half-tag-space-in-flight rule; anything ambiguous
// isolates the queue instead of guessing the most recent request.
type Ring struct {
	slots     []slotState
	mask      uint64 // tag mask, (1<<TruncBits)-1
	half      uint64 // half the tag space: max in-flight window
	seqSpace  uint64 // 1<<SeqBits
	onRelease ReleaseFunc

	subSeq   uint64 // next sequence to assign, within the current epoch
	epoch    uint64
	inFlight int
	isolated bool

	stats Stats
}

// NewRing validates cfg and returns an empty ring.
func NewRing(cfg Config) (*Ring, error) {
	if cfg.Slots < 2 || cfg.Slots&(cfg.Slots-1) != 0 {
		return nil, fmt.Errorf("netsec: Slots must be a power of two >= 2, got %d", cfg.Slots)
	}
	if cfg.TruncBits < 1 || cfg.TruncBits > 32 {
		return nil, fmt.Errorf("netsec: TruncBits must be in [1,32], got %d", cfg.TruncBits)
	}
	if tagSpace := uint64(1) << cfg.TruncBits; tagSpace < 2*uint64(cfg.Slots) {
		return nil, fmt.Errorf("netsec: tag space 2^%d must be at least twice the ring size %d", cfg.TruncBits, cfg.Slots)
	}
	if cfg.SeqBits <= cfg.TruncBits || cfg.SeqBits > 63 {
		return nil, fmt.Errorf("netsec: SeqBits must be in (TruncBits,63], got %d", cfg.SeqBits)
	}
	return &Ring{
		slots:     make([]slotState, cfg.Slots),
		mask:      (uint64(1) << cfg.TruncBits) - 1,
		half:      uint64(1) << (cfg.TruncBits - 1),
		seqSpace:  uint64(1) << cfg.SeqBits,
		onRelease: cfg.OnRelease,
	}, nil
}

// Submit binds the next ring slot to (tenant, lease) and returns the slot
// index to program into the hardware descriptor.
func (r *Ring) Submit(tenant Tenant, lease Lease) (int, error) {
	if r.isolated {
		return -1, ErrQueueIsolated
	}
	if r.subSeq >= r.seqSpace {
		return -1, ErrSeqExhausted
	}
	if r.inFlight > 0 && r.subSeq-r.oldestSeq() >= r.half {
		// The in-flight span already covers half the tag space; admitting
		// more would make truncated tags ambiguous. Backpressure until the
		// oldest request completes.
		return -1, ErrQueueFull
	}
	slot := int(r.subSeq) & (len(r.slots) - 1)
	s := &r.slots[slot]
	if s.inFlight {
		return -1, ErrQueueFull
	}
	*s = slotState{inFlight: true, seq: r.subSeq, tenant: tenant, lease: lease}
	r.subSeq++
	r.inFlight++
	r.stats.Submitted++
	return slot, nil
}

// Complete processes one hardware completion: the slot index and the
// truncated generation tag the device reported.
//
// A completion whose tag matches no in-flight request (duplicate, late, or
// left over from before a reset) is ignored and counted in Stats.Stale; it
// never releases a lease. A completion whose tag names a live request on a
// different slot, an out-of-range tag, or a bad slot index is ambiguous: the
// queue is isolated and ErrQueueIsolated is returned. The ring never guesses
// the most recent request.
func (r *Ring) Complete(slot int, tag uint64) error {
	if r.isolated {
		return ErrQueueIsolated
	}
	if slot < 0 || slot >= len(r.slots) || tag > r.mask {
		return r.isolate()
	}
	cand, ok := r.recoverSeq(tag)
	if !ok {
		// No in-flight sequence can carry this tag: stale or garbage.
		r.stats.Stale++
		return nil
	}
	home := &r.slots[int(cand)&(len(r.slots)-1)]
	if !home.inFlight || home.seq != cand {
		// The tag belongs to a request that already completed: a duplicate
		// or a late straggler. Nothing to release.
		r.stats.Stale++
		return nil
	}
	// The tag names a live request; it must live on the addressed slot.
	s := &r.slots[slot]
	if !s.inFlight || s.seq != cand {
		return r.isolate()
	}
	s.inFlight = false
	r.inFlight--
	r.stats.Completed++
	r.release(s.tenant, s.lease)
	return nil
}

// Reset handles a device reset: every in-flight lease is released exactly
// once, the isolation latch is cleared, and a new epoch begins. Completions
// arriving afterwards for pre-reset requests find no in-flight binding and
// are ignored as stale.
func (r *Ring) Reset() {
	for i := range r.slots {
		s := &r.slots[i]
		if s.inFlight {
			s.inFlight = false
			r.release(s.tenant, s.lease)
		}
	}
	r.inFlight = 0
	r.subSeq = 0
	r.epoch++
	r.isolated = false
	r.stats.Resets++
}

// SwitchEpoch starts a new epoch after the submission sequence space was
// exhausted. The queue must be fully drained first so no in-flight request
// of the old epoch can alias a reused sequence number.
func (r *Ring) SwitchEpoch() error {
	if r.isolated {
		return ErrQueueIsolated
	}
	if r.inFlight > 0 {
		return ErrNotDrained
	}
	r.subSeq = 0
	r.epoch++
	r.stats.EpochSwitches++
	return nil
}

// Isolated reports whether the queue is quarantined after an ambiguous
// completion. Only Reset clears the latch.
func (r *Ring) Isolated() bool { return r.isolated }

// InFlight returns the number of submitted requests awaiting completion.
func (r *Ring) InFlight() int { return r.inFlight }

// Epoch returns the current epoch.
func (r *Ring) Epoch() uint64 { return r.epoch }

// Stats returns a snapshot of the ring counters.
func (r *Ring) Stats() Stats { return r.stats }

// recoverSeq maps a truncated tag back to a full sequence number inside the
// recovery window (subSeq-half, subSeq). Because at most half of the tag
// space is ever in flight, at most one candidate exists.
func (r *Ring) recoverSeq(tag uint64) (uint64, bool) {
	if r.inFlight == 0 {
		return 0, false
	}
	hi := r.subSeq - 1
	var lo uint64
	if hi+1 > r.half {
		lo = hi + 1 - r.half
	}
	cand := lo + ((tag - (lo & r.mask)) & r.mask)
	if cand > hi {
		return 0, false
	}
	return cand, true
}

func (r *Ring) isolate() error {
	r.isolated = true
	r.stats.Isolations++
	return ErrQueueIsolated
}

func (r *Ring) release(tenant Tenant, lease Lease) {
	r.stats.Released++
	if r.onRelease != nil {
		r.onRelease(tenant, lease)
	}
}

// oldestSeq returns the oldest in-flight sequence; only called when
// inFlight > 0.
func (r *Ring) oldestSeq() uint64 {
	oldest := r.subSeq
	for i := range r.slots {
		if s := &r.slots[i]; s.inFlight && s.seq < oldest {
			oldest = s.seq
		}
	}
	return oldest
}
