// Package genring implements a hardware completion ring manager for an I/O
// driver host.
//
// Each ring slot is bound to three identities at submission time:
//
//   - the full submission generation (epoch and per-epoch sequence),
//   - the buffer lease that owns the DMA buffers,
//   - the tenant that owns the request.
//
// Hardware only reports a truncated generation tag in the completion entry.
// The host reconstructs the full generation using the fact that at most half
// of the ring may be in flight at any instant, so the generations that can
// still complete lie inside a window no wider than the ring itself. When the
// truncated tag is ambiguous inside that window the queue is quarantined
// instead of guessing (for example, never assume the tag belongs to the most
// recently submitted request).
//
// When the per-epoch submission sequence is exhausted the ring stops
// accepting submissions, drains all in-flight slots, and only then rolls over
// to a fresh epoch.
//
// Concurrency: Ring is not internally synchronized. A driver host typically
// owns one goroutine for the submission path and one for the completion path;
// the host must serialize access (for example with a mutex or a single event
// loop) because submissions and completions mutate the same slot array.
package genring

import (
	"errors"
	"math/bits"
)

// Tenant identifies the workload owner of a submitted request.
type Tenant uint64

// Lease identifies a buffer lease. A lease is released exactly once per
// accepted completion, through the configured OnRelease callback.
type Lease uint64

// Generation is the full, untruncated submission generation. The low 32 bits
// are the per-epoch submission sequence and the high 32 bits are the epoch.
type Generation uint64

// Epoch returns the epoch component of the generation.
func (g Generation) Epoch() uint32 { return uint32(g >> 32) }

// Sequence returns the per-epoch sequence component of the generation.
func (g Generation) Sequence() uint32 { return uint32(g) }

// makeGeneration packs an epoch and a sequence into a full generation.
func makeGeneration(epoch, seq uint32) Generation {
	return Generation(uint64(epoch)<<32 | uint64(seq))
}

// ReleaseReason explains why a lease is being handed back to the host.
type ReleaseReason uint8

const (
	// ReleaseCompleted means the device reported a normal completion.
	ReleaseCompleted ReleaseReason = iota + 1
	// ReleaseReset means the device was reset while the lease was in flight.
	ReleaseReset
	// ReleaseQuarantine means the queue was isolated and the lease must be
	// recovered rather than returned to a free pool blindly.
	ReleaseQuarantine
)

// Errors reported by Ring methods. All of these may be compared with errors.Is.
var (
	// ErrIsolated is returned after the queue has been quarantined. Only
	// Reset can return a quarantined ring to service.
	ErrIsolated = errors.New("genring: queue is quarantined")
	// ErrNoSlot is returned when no ring slot is available for submission.
	ErrNoSlot = errors.New("genring: no free ring slot")
	// ErrSequenceExhausted is returned when the per-epoch sequence space has
	// been consumed and the ring has armed its drain state. While the epoch
	// still has occupied slots, Submit returns ErrDraining instead; after the
	// ring has fully drained both sentinels describe the same blocked state.
	ErrSequenceExhausted = errors.New("genring: submission sequence exhausted")
	// ErrDraining is returned while an exhausted epoch still has in-flight
	// slots; submissions remain blocked until Rollover completes the epoch.
	ErrDraining = errors.New("genring: epoch draining before rollover")
	// ErrStaleCompletion is returned for a completion whose truncated tag
	// matches no in-flight slot. It is a late completion from a reused slot.
	ErrStaleCompletion = errors.New("genring: stale or late completion")
	// ErrDuplicateCompletion is returned when a completion is delivered twice
	// for the same slot and generation. No lease is released by the duplicate.
	ErrDuplicateCompletion = errors.New("genring: duplicate completion")
	// ErrAmbiguousGeneration is returned when a truncated tag matches more
	// than one in-flight generation. The queue is quarantined.
	ErrAmbiguousGeneration = errors.New("genring: truncated generation is ambiguous")
	// ErrSlotMismatch is returned when the slot index carried by a completion
	// does not match the unique slot owning the reported generation. The queue
	// is quarantined because the completion stream cannot be trusted.
	ErrSlotMismatch = errors.New("genring: completion slot does not match generation")
	// ErrInFlight is returned when Rollover is requested before the exhausted
	// epoch has fully drained.
	ErrInFlight = errors.New("genring: cannot roll over while slots are in flight")
	// ErrNotExhausted is returned when Rollover is requested outside of the
	// sequence-exhausted drain state.
	ErrNotExhausted = errors.New("genring: rollover only valid after sequence exhaustion")
	// ErrEpochExhausted is returned when no further epoch number is available.
	ErrEpochExhausted = errors.New("genring: epoch space exhausted")
)

// Config declares the ring geometry and the lease release callback.
type Config struct {
	// Size is the number of ring slots. It must be a power of two and at
	// least two.
	Size int
	// TagBits is the width, in bits, of the truncated generation tag that
	// hardware reports. With at most half the ring in flight, TagBits equal
	// to or greater than log2(Size)+1 guarantees that any completion can be
	// attributed unambiguously. Narrower tags are allowed so the ambiguity
	// path can be exercised and tested.
	TagBits int
	// MaxInflight, when positive, caps the number of concurrently in-flight
	// slots. Zero means Size/2 (the maximum safe depth for generation
	// recovery). Values above Size/2 are rejected.
	MaxInflight int
	// OnRelease is invoked exactly once per lease that leaves the ring,
	// whether through completion, reset, or quarantine. It must not call back
	// into the Ring.
	OnRelease func(lease Lease, tenant Tenant, gen Generation, reason ReleaseReason)
}

// Slot is the host-side state of one ring entry.
type Slot struct {
	inFlight  bool
	completed bool
	gen       Generation
	lease     Lease
	tenant    Tenant
	// lastTag records the truncated tag of the most recent completion while
	// the slot remains occupied, so duplicate CQEs can be identified even
	// before the slot is reclaimed.
	lastTag uint64
	hasLast bool
	// retired marks a reclaimed free slot: its retained lastTag can only
	// describe a late completion, never a duplicate of an occupied entry.
	retired bool
	// pos is the absolute submission position, useful for diagnostics.
	pos uint64
}

// Gen returns the generation bound to the slot.
func (s Slot) Gen() Generation { return s.gen }

// Lease returns the buffer lease bound to the slot.
func (s Slot) Lease() Lease { return s.lease }

// Tenant returns the tenant bound to the slot.
func (s Slot) Tenant() Tenant { return s.tenant }

// InFlight reports whether the slot is awaiting a device completion.
func (s Slot) InFlight() bool { return s.inFlight }

// Completed reports whether the slot holds an acknowledged completion that
// has not yet been reclaimed.
func (s Slot) Completed() bool { return s.completed }

// CompletionResult describes the outcome of feeding one hardware completion
// entry to the ring.
type CompletionResult struct {
	// Slot is the index of the attributed slot. It is -1 when the entry could
	// not be attributed (stale, duplicate, or quarantined).
	Slot int
	// Generation is the reconstructed full generation. It is zero when the
	// entry could not be attributed.
	Generation Generation
	// Released reports whether this call released a lease. Duplicate and stale
	// completions never release a lease.
	Released bool
	// Duplicate reports an already-completed entry delivered again.
	Duplicate bool
	// Stale reports a late entry whose generation no longer exists in the ring.
	Stale bool
	// Quarantined reports that this call isolated the queue.
	Quarantined bool
}

// Stats is a point-in-time snapshot of ring counters.
type Stats struct {
	Inflight    uint64
	Completed   uint64
	Submitted   uint64
	Released    uint64
	Duplicates  uint64
	Stale       uint64
	Resets      uint64
	Quarantines uint64
	Epoch       uint32
	Sequence    uint32
	Isolated    bool
	Draining    bool
}

// Ring owns the slot array and the generation bookkeeping.
type Ring struct {
	slots       []Slot
	size        int
	tagMask     uint64
	tagBits     int
	maxInflight int
	onRelease   func(Lease, Tenant, Generation, ReleaseReason)

	tail uint64 // absolute position of the next reclaimable slot
	next uint64 // absolute position of the next submission

	inflight  int
	completed int

	epoch    uint32
	sequence uint32
	draining bool
	isolated bool

	// seqCeiling bounds the per-epoch sequence. It is MaxUint32 in production
	// and may be lowered by tests to exercise epoch rollover without issuing
	// four billion requests.
	seqCeiling uint32

	submitted   uint64
	released    uint64
	duplicates  uint64
	stale       uint64
	resets      uint64
	quarantines uint64
}

// New validates the configuration and allocates a ring.
func New(cfg Config) (*Ring, error) {
	if cfg.Size < 2 || cfg.Size&(cfg.Size-1) != 0 {
		return nil, errors.New("genring: Size must be a power of two >= 2")
	}
	if cfg.TagBits < 1 || cfg.TagBits > 64 {
		return nil, errors.New("genring: TagBits must be in [1,64]")
	}
	maxInflight := cfg.MaxInflight
	if maxInflight == 0 {
		maxInflight = cfg.Size / 2
	}
	if maxInflight < 1 || maxInflight > cfg.Size/2 {
		return nil, errors.New("genring: MaxInflight must be in [1, Size/2]")
	}
	if cfg.OnRelease == nil {
		return nil, errors.New("genring: OnRelease callback is required")
	}
	mask := uint64(1)<<uint(cfg.TagBits) - 1
	if cfg.TagBits == 64 {
		mask = ^uint64(0)
	}
	return &Ring{
		slots:       make([]Slot, cfg.Size),
		size:        cfg.Size,
		tagMask:     mask,
		tagBits:     cfg.TagBits,
		maxInflight: maxInflight,
		onRelease:   cfg.OnRelease,
		seqCeiling:  ^uint32(0),
	}, nil
}

// SafeTagBits returns the tag width that guarantees unambiguous attribution
// for a ring of the given power-of-two size under the half-ring in-flight
// limit.
func SafeTagBits(size int) int {
	return bits.Len(uint(size))
}

// Tag returns the truncated hardware tag for a full generation.
func (r *Ring) Tag(gen Generation) uint64 {
	return uint64(gen) & r.tagMask
}

// Isolated reports whether the queue has been quarantined.
func (r *Ring) Isolated() bool { return r.isolated }

// Draining reports whether an exhausted epoch is waiting to drain.
func (r *Ring) Draining() bool { return r.draining }

// Epoch returns the current epoch.
func (r *Ring) Epoch() uint32 { return r.epoch }

// TagBits reports the configured truncated-tag width.
func (r *Ring) TagBits() int { return r.tagBits }

// Inflight returns the number of slots awaiting completion.
func (r *Ring) Inflight() int { return r.inflight }

// Len returns the number of occupied slots (in flight plus completed but not
// yet reclaimed).
func (r *Ring) Len() int { return int(r.next - r.tail) }

// FreeSlots returns the number of slots that could be submitted now.
func (r *Ring) FreeSlots() int {
	return r.maxInflight - r.Len()
}

// Slot returns a read-only copy of the slot at the given index.
func (r *Ring) Slot(index int) Slot {
	return r.slots[index&(r.size-1)]
}

// Stats snapshots the ring counters.
func (r *Ring) Stats() Stats {
	return Stats{
		Inflight:    uint64(r.inflight),
		Completed:   uint64(r.completed),
		Submitted:   r.submitted,
		Released:    r.released,
		Duplicates:  r.duplicates,
		Stale:       r.stale,
		Resets:      r.resets,
		Quarantines: r.quarantines,
		Epoch:       r.epoch,
		Sequence:    r.sequence,
		Isolated:    r.isolated,
		Draining:    r.draining,
	}
}

// Submit binds a lease and tenant to the next free slot and returns the
// allocated slot index and full generation. The caller programs the hardware
// descriptor with the slot index and r.Tag(gen).
func (r *Ring) Submit(lease Lease, tenant Tenant) (int, Generation, error) {
	if r.isolated {
		return 0, 0, ErrIsolated
	}
	if r.draining {
		if r.Len() != 0 {
			return 0, 0, ErrDraining
		}
		return 0, 0, ErrSequenceExhausted
	}
	if r.Len() >= r.maxInflight {
		return 0, 0, ErrNoSlot
	}

	index := int(r.next) & (r.size - 1)
	r.sequence++
	if r.sequence >= r.seqCeiling {
		// The last usable sequence of this epoch has just been issued. Further
		// submissions are blocked until the epoch drains and rolls over.
		r.draining = true
	}
	gen := makeGeneration(r.epoch, r.sequence)
	slot := &r.slots[index]
	slot.inFlight = true
	slot.completed = false
	slot.gen = gen
	slot.lease = lease
	slot.tenant = tenant
	slot.hasLast = false
	slot.lastTag = 0
	slot.retired = false
	slot.pos = r.next

	r.next++
	r.inflight++
	r.submitted++
	return index, gen, nil
}

// Complete feeds one hardware completion entry to the ring. index is the slot
// index reported by hardware and tag is the truncated generation.
//
// The full generation is recovered by scanning the in-flight window, which
// spans at most half the ring. If the tag matches several in-flight
// generations the queue is quarantined rather than picking a candidate.
func (r *Ring) Complete(index int, tag uint64) (CompletionResult, error) {
	res := CompletionResult{Slot: -1}
	if r.isolated {
		return res, ErrIsolated
	}
	if index < 0 || index >= r.size {
		r.quarantine()
		res.Quarantined = true
		return res, ErrSlotMismatch
	}

	tag &= r.tagMask

	// Recover the full generation by scanning every in-flight slot. The
	// in-flight window spans at most half the ring, so with a SafeTagBits tag
	// at most one candidate can exist; narrower tags are detected here too.
	var candidates []int
	for i := range r.slots {
		slot := &r.slots[i]
		if slot.inFlight && r.Tag(slot.gen) == tag {
			candidates = append(candidates, i)
		}
	}

	switch len(candidates) {
	case 0:
		// Nothing in flight carries this tag. If the named slot already
		// completed the same tag it is a duplicate; otherwise the entry is a
		// late completion from a slot that has since been reused or reset.
		r.stale++
		res.Stale = true
		slot := &r.slots[index]
		if slot.hasLast && !slot.retired && slot.lastTag == tag {
			r.duplicates++
			res.Stale = false
			res.Duplicate = true
			return res, ErrDuplicateCompletion
		}
		return res, ErrStaleCompletion
	case 1:
		owner := candidates[0]
		if owner != index {
			// A unique in-flight generation matches, but hardware pointed at a
			// different slot. The completion stream is internally inconsistent.
			r.quarantine()
			res.Quarantined = true
			return res, ErrSlotMismatch
		}
		return r.finish(owner, r.slots[owner].gen), nil
	default:
		// The truncated tag cannot be attributed uniquely. Never guess, and in
		// particular never assume the most recent request.
		r.quarantine()
		res.Quarantined = true
		return res, ErrAmbiguousGeneration
	}
}

// finish marks one in-flight slot as completed and releases its lease exactly
// once. Reclaimable prefix slots are freed for reuse.
func (r *Ring) finish(index int, gen Generation) CompletionResult {
	slot := &r.slots[index]
	slot.inFlight = false
	slot.completed = true
	slot.hasLast = true
	slot.lastTag = r.Tag(gen)
	r.inflight--
	r.completed++

	res := CompletionResult{
		Slot:       index,
		Generation: gen,
	}
	before := r.released
	r.reclaim()
	res.Released = r.released > before
	// The lease is released only when reclaim actually retires the slot, so a
	// completed-but-still-occupied slot keeps its identity for duplicate
	// detection and guarantees exactly one release per lease.
	return res
}

// reclaim retires completed slots once the ring has no in-flight work. A
// completed slot whose window still contains an in-flight slot stays occupied
// so its generation and completion tag remain available for duplicate and
// late-completion classification.
func (r *Ring) reclaim() {
	// While any slot is in flight, completed slots stay occupied; this keeps
	// the half-ring attribution window and duplicate detection intact.
	if r.inflight > 0 {
		return
	}
	for r.tail != r.next {
		index := int(r.tail) & (r.size - 1)
		slot := &r.slots[index]
		if slot.completed {
			slot.completed = false
			// Retire the occupant but keep the last completion tag while the
			// slot is free: matching CQEs are then classified as late (stale)
			// rather than as duplicates of an entry that no longer exists.
			r.release(slot, ReleaseCompleted)
			slot.gen = 0
			slot.lease = 0
			slot.tenant = 0
			slot.pos = 0
			slot.retired = true
			r.completed--
		}
		r.tail++
	}
}

// release invokes the host callback and counts the hand-back. All lease
// releases in the package go through this helper so the exactly-once
// invariant is enforced by the slot state transitions around it.
func (r *Ring) release(slot *Slot, reason ReleaseReason) {
	r.onRelease(slot.lease, slot.tenant, slot.gen, reason)
	r.released++
}

// quarantine isolates the queue and returns every occupied lease to the host
// for recovery. A slot completed out of order may still hold an unreleased
// lease because reclaim is blocked by an in-flight slot ahead of it; those
// leases are recovered here as well. Retired (already released) slots are
// merely cleared.
func (r *Ring) quarantine() {
	if r.isolated {
		return
	}
	r.isolated = true
	r.quarantines++
	for i := range r.slots {
		slot := &r.slots[i]
		if slot.inFlight || slot.completed {
			wasInflight := slot.inFlight
			slot.inFlight = false
			if wasInflight {
				r.inflight--
			}
			r.release(slot, ReleaseQuarantine)
		}
		slot.completed = false
		slot.gen = 0
		slot.lease = 0
		slot.tenant = 0
		slot.hasLast = false
		slot.lastTag = 0
		slot.retired = false
		slot.pos = 0
	}
	r.completed = 0
	r.tail = r.next
}

// Rollover finishes an exhausted epoch. It may run only after every slot of
// the epoch has drained and been reclaimed, implementing "drain before epoch
// change". It returns the new epoch number.
func (r *Ring) Rollover() (uint32, error) {
	if r.isolated {
		return 0, ErrIsolated
	}
	if !r.draining {
		return 0, ErrNotExhausted
	}
	if r.inflight != 0 || r.completed != 0 || r.tail != r.next {
		return 0, ErrInFlight
	}
	if r.epoch == ^uint32(0) {
		return 0, ErrEpochExhausted
	}
	r.epoch++
	r.sequence = 0
	r.draining = false
	// The old epoch fully drained, so the new epoch starts on a clean ring
	// with absolute positions re-based to zero.
	r.tail = 0
	r.next = 0
	return r.epoch, nil
}

// Reset models a device reset: every in-flight lease is handed back with
// ReleaseReset, the slot array is emptied, and the epoch advances so that
// completion entries emitted before the reset can never match a post-reset
// generation. A quarantined ring returns to service through this call.
//
// The driver host must only reset a device after quiescing it and must keep
// the completion queue drained until the device acknowledges the reset; a
// truncated tag alone cannot distinguish a pre-reset entry that collides with
// a post-reset tag, so the hardware/driver reset barrier is part of the
// contract.
func (r *Ring) Reset() (uint32, error) {
	if r.epoch == ^uint32(0) {
		return 0, ErrEpochExhausted
	}
	for i := range r.slots {
		slot := &r.slots[i]
		if slot.inFlight || slot.completed {
			wasInflight := slot.inFlight
			slot.inFlight = false
			if wasInflight {
				r.inflight--
			}
			r.release(slot, ReleaseReset)
		}
		slot.completed = false
		slot.gen = 0
		slot.lease = 0
		slot.tenant = 0
		slot.hasLast = false
		slot.lastTag = 0
		slot.retired = false
		slot.pos = 0
	}
	r.tail = r.next
	r.completed = 0
	r.epoch++
	r.sequence = 0
	r.draining = false
	r.isolated = false
	r.resets++
	return r.epoch, nil
}
