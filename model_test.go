package netsec

import (
	"math/rand"
	"testing"
)

// model is an independent specification of the ring semantics that works
// with full sequence numbers only (no truncation, no recovery window).
// The exhaustive and fuzz tests compare the ring against it operation by
// operation.
type model struct {
	slots    []modelSlot
	subSeq   uint64
	seqSpace uint64
	mask     uint64
	half     uint64
	inFlight int
	isolated bool
	stale    uint64

	expected  []releaseEvent // leases the spec released, in order
	nextLease Lease
	lastTag   []uint64 // tag of the most recent completed occupant per slot
	hasLast   []bool
}

type modelSlot struct {
	inFlight bool
	seq      uint64
	tenant   Tenant
	lease    Lease
}

func newModel(cfg Config) *model {
	return &model{
		slots:    make([]modelSlot, cfg.Slots),
		seqSpace: uint64(1) << cfg.SeqBits,
		mask:     (uint64(1) << cfg.TruncBits) - 1,
		half:     uint64(1) << (cfg.TruncBits - 1),
		lastTag:  make([]uint64, cfg.Slots),
		hasLast:  make([]bool, cfg.Slots),
	}
}

func (m *model) clone() *model {
	c := *m
	c.slots = append([]modelSlot(nil), m.slots...)
	c.expected = append([]releaseEvent(nil), m.expected...)
	c.lastTag = append([]uint64(nil), m.lastTag...)
	c.hasLast = append([]bool(nil), m.hasLast...)
	return &c
}

func (m *model) submit(tenant Tenant, lease Lease) (int, bool) {
	if m.isolated || m.subSeq >= m.seqSpace {
		return -1, false
	}
	if m.inFlight > 0 && m.subSeq-m.oldest() >= m.half {
		return -1, false
	}
	slot := int(m.subSeq) & (len(m.slots) - 1)
	if m.slots[slot].inFlight {
		return -1, false
	}
	m.slots[slot] = modelSlot{inFlight: true, seq: m.subSeq, tenant: tenant, lease: lease}
	m.subSeq++
	m.inFlight++
	return slot, true
}

func (m *model) oldest() uint64 {
	oldest := m.subSeq
	for i := range m.slots {
		if m.slots[i].inFlight && m.slots[i].seq < oldest {
			oldest = m.slots[i].seq
		}
	}
	return oldest
}

func (m *model) complete(slot int, tag uint64) {
	if m.isolated {
		return
	}
	if slot < 0 || slot >= len(m.slots) || tag > m.mask {
		m.isolated = true
		return
	}
	s := &m.slots[slot]
	if s.inFlight && s.seq&m.mask == tag {
		m.expected = append(m.expected, releaseEvent{s.tenant, s.lease})
		m.lastTag[slot] = tag
		m.hasLast[slot] = true
		s.inFlight = false
		m.inFlight--
		return
	}
	// Ambiguous only if the tag names a different live request.
	for i := range m.slots {
		if m.slots[i].inFlight && m.slots[i].seq&m.mask == tag {
			m.isolated = true
			return
		}
	}
	m.stale++
}

func (m *model) reset() {
	for i := range m.slots {
		if m.slots[i].inFlight {
			m.expected = append(m.expected, releaseEvent{m.slots[i].tenant, m.slots[i].lease})
			m.slots[i].inFlight = false
		}
	}
	m.inFlight = 0
	m.subSeq = 0
	m.isolated = false
}

func (m *model) switchEpoch() bool {
	if m.isolated || m.inFlight > 0 {
		return false
	}
	m.subSeq = 0
	return true
}

func cloneRing(r *Ring) *Ring {
	c := *r
	c.slots = append([]slotState(nil), r.slots...)
	return &c
}

// checkState asserts the ring matches the model and that every lease has
// been released at most once.
func checkState(t *testing.T, r *Ring, m *model, rec *recorder) {
	t.Helper()
	if r.Isolated() != m.isolated {
		t.Fatalf("isolated: ring=%v model=%v", r.Isolated(), m.isolated)
	}
	if r.InFlight() != m.inFlight {
		t.Fatalf("inFlight: ring=%d model=%d", r.InFlight(), m.inFlight)
	}
	if len(rec.events) != len(m.expected) {
		t.Fatalf("releases: ring=%v model=%v", rec.events, m.expected)
	}
	counts := map[Lease]int{}
	for i, e := range rec.events {
		if e != m.expected[i] {
			t.Fatalf("release %d: ring=%v model=%v", i, e, m.expected[i])
		}
		counts[e.lease]++
		if counts[e.lease] > 1 {
			t.Fatalf("lease %d released twice: %v", e.lease, rec.events)
		}
	}
	if st := r.Stats(); st.Released != uint64(len(rec.events)) || st.Stale != m.stale {
		t.Fatalf("stats %+v vs releases=%d stale=%d", st, len(rec.events), m.stale)
	}
}

type opKind int

const (
	opSubmit opKind = iota
	opComplete
	opReset
	opEpoch
)

type op struct {
	kind opKind
	slot int
	tag  uint64
}

// genOps enumerates the operations to explore from the current state:
// submit, every valid completion, every one-cycle-late duplicate completion
// (the previous occupant's tag), reset, and epoch switch. Completions
// delayed by two or more full ring cycles are outside the truncation
// safety contract and are not generated.
func genOps(m *model, faults bool) []op {
	ops := []op{{kind: opSubmit}, {kind: opReset}}
	if m.inFlight == 0 && !m.isolated {
		ops = append(ops, op{kind: opEpoch})
	}
	for slot := range m.slots {
		if m.slots[slot].inFlight {
			ops = append(ops, op{opComplete, slot, m.slots[slot].seq & m.mask})
		}
		if m.hasLast[slot] {
			ops = append(ops, op{opComplete, slot, m.lastTag[slot]})
		}
	}
	if faults {
		ops = append(ops,
			op{opComplete, 0, ^uint64(0)},   // tag out of range
			op{opComplete, len(m.slots), 0}, // slot out of range
		)
		for slot := range m.slots {
			if m.slots[slot].inFlight {
				// A live request's tag addressed to the wrong slot.
				ops = append(ops, op{opComplete, (slot + 1) % len(m.slots), m.slots[slot].seq & m.mask})
				break
			}
		}
	}
	return ops
}

// explore runs a depth-limited exhaustive DFS over operation sequences,
// checking the ring against the model at every reachable state.
func explore(t *testing.T, cfg Config, depth int, faults bool) (nodes int) {
	t.Helper()
	rec := &recorder{}
	cfg.OnRelease = rec.release
	root, err := NewRing(cfg)
	if err != nil {
		t.Fatalf("NewRing: %v", err)
	}
	var walk func(r *Ring, m *model, d int)
	walk = func(r *Ring, m *model, d int) {
		nodes++
		checkState(t, r, m, rec)
		if d == 0 {
			return
		}
		for _, o := range genOps(m, faults) {
			r2, m2 := cloneRing(r), m.clone()
			mark := len(rec.events)
			switch o.kind {
			case opSubmit:
				lease, tenant := m2.nextLease, Tenant(m2.nextLease%3)
				m2.nextLease++
				mslot, mok := m2.submit(tenant, lease)
				rslot, rerr := r2.Submit(tenant, lease)
				if mok != (rerr == nil) || (mok && mslot != rslot) {
					t.Fatalf("submit: model=(%d,%v) ring=(%d,%v)", mslot, mok, rslot, rerr)
				}
			case opComplete:
				m2.complete(o.slot, o.tag)
				rerr := r2.Complete(o.slot, o.tag)
				if (rerr == ErrQueueIsolated) != m2.isolated {
					t.Fatalf("complete(%d,%d): ring err=%v model isolated=%v", o.slot, o.tag, rerr, m2.isolated)
				}
			case opReset:
				m2.reset()
				r2.Reset()
			case opEpoch:
				mok := m2.switchEpoch()
				rerr := r2.SwitchEpoch()
				if mok != (rerr == nil) {
					t.Fatalf("switchEpoch: model=%v ring=%v", mok, rerr)
				}
			}
			walk(r2, m2, d-1)
			rec.events = rec.events[:mark]
		}
	}
	walk(root, newModel(cfg), depth)
	return nodes
}

// TestExhaustiveExploration enumerates all interleavings of submissions,
// out-of-order completions, duplicate and one-cycle-late completions,
// device resets, and epoch switches on a small bit-width ring, verifying
// that every lease is released exactly once and only by its own completion
// or retirement.
func TestExhaustiveExploration(t *testing.T) {
	cfg := Config{Slots: 4, TruncBits: 3, SeqBits: 4}
	nodes := explore(t, cfg, 8, false)
	t.Logf("explored %d states", nodes)
}

// TestExhaustiveExplorationWithFaults additionally injects out-of-range
// tags, bad slot indices, and wrong-slot completions (which must isolate).
func TestExhaustiveExplorationWithFaults(t *testing.T) {
	cfg := Config{Slots: 4, TruncBits: 3, SeqBits: 4}
	nodes := explore(t, cfg, 6, true)
	t.Logf("explored %d states", nodes)
}

// TestModelFuzz drives long random operation sequences on a larger
// bit-width ring and checks the same invariants after every step.
func TestModelFuzz(t *testing.T) {
	var totalIsolations, totalEpochSwitches, totalStale uint64
	for seed := int64(0); seed < 100; seed++ {
		rng := rand.New(rand.NewSource(seed))
		cfg := Config{Slots: 8, TruncBits: 5, SeqBits: 8} // small seq space: exercises exhaustion + epoch switches
		rec := &recorder{}
		cfg.OnRelease = rec.release
		r, err := NewRing(cfg)
		if err != nil {
			t.Fatalf("NewRing: %v", err)
		}
		m := newModel(cfg)
		for step := 0; step < 2000; step++ {
			switch n := rng.Intn(100); {
			case n < 40: // submit
				lease, tenant := m.nextLease, Tenant(m.nextLease%3)
				mslot, mok := m.submit(tenant, lease)
				rslot, rerr := r.Submit(tenant, lease)
				if mok != (rerr == nil) || (mok && mslot != rslot) {
					t.Fatalf("seed %d step %d: submit mismatch", seed, step)
				}
				if mok {
					m.nextLease++
				}
			case n < 70: // valid completion of a random in-flight slot
				var live []int
				for i := range m.slots {
					if m.slots[i].inFlight {
						live = append(live, i)
					}
				}
				if len(live) > 0 {
					slot := live[rng.Intn(len(live))]
					tag := m.slots[slot].seq & m.mask
					m.complete(slot, tag)
					rerr := r.Complete(slot, tag)
					if (rerr == ErrQueueIsolated) != m.isolated {
						t.Fatalf("seed %d step %d: valid complete: %v", seed, step, rerr)
					}
				}
			case n < 85: // duplicate / one-cycle-late completion
				slot := rng.Intn(len(m.slots))
				if m.hasLast[slot] {
					tag := m.lastTag[slot]
					m.complete(slot, tag)
					rerr := r.Complete(slot, tag)
					if (rerr == ErrQueueIsolated) != m.isolated {
						t.Fatalf("seed %d step %d: dup complete: %v", seed, step, rerr)
					}
				}
			case n < 90: // device reset
				m.reset()
				r.Reset()
			case n < 95: // epoch switch attempt
				mok := m.switchEpoch()
				rerr := r.SwitchEpoch()
				if mok != (rerr == nil) {
					t.Fatalf("seed %d step %d: epoch mismatch", seed, step)
				}
			default: // garbage completion
				slot := rng.Intn(len(m.slots) + 2)
				tag := rng.Uint64() & (m.mask + 1) // sometimes out of range
				m.complete(slot, tag)
				rerr := r.Complete(slot, tag)
				if (rerr == ErrQueueIsolated) != m.isolated {
					t.Fatalf("seed %d step %d: isolation mismatch", seed, step)
				}
			}
			checkState(t, r, m, rec)
		}
		// Drain and reset: every submitted lease must end released exactly once.
		if !m.isolated {
			for i := range m.slots {
				if m.slots[i].inFlight {
					tag := m.slots[i].seq & m.mask
					m.complete(i, tag)
					if err := r.Complete(i, tag); err != nil {
						t.Fatalf("seed %d drain: %v", seed, err)
					}
				}
			}
		}
		m.reset()
		r.Reset()
		checkState(t, r, m, rec)
		counts := map[Lease]int{}
		for _, e := range rec.events {
			counts[e.lease]++
		}
		for l := Lease(0); l < m.nextLease; l++ {
			if counts[l] != 1 {
				t.Fatalf("seed %d: lease %d released %d times", seed, l, counts[l])
			}
		}
		st := r.Stats()
		totalIsolations += st.Isolations
		totalEpochSwitches += st.EpochSwitches
		totalStale += st.Stale
	}
	if totalIsolations == 0 || totalEpochSwitches == 0 || totalStale == 0 {
		t.Fatalf("fuzz coverage: isolations=%d epochSwitches=%d stale=%d",
			totalIsolations, totalEpochSwitches, totalStale)
	}
}
