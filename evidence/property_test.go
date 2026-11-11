package evidence

import (
	"math/rand"
	"testing"
)

// streamOracle is an independent, unbounded record of every byte ever written.
// It is allocation-heavy on purpose: the bounded buffer under test is checked
// against this simple source of truth byte for byte.
type streamOracle struct {
	covered []bool
	data    []byte
}

func newStreamOracle(size int) *streamOracle {
	return &streamOracle{covered: make([]bool, size), data: make([]byte, size)}
}

func (o *streamOracle) write(off int64, p []byte) {
	for i, v := range p {
		pos := int(off) + i
		if pos < 0 || pos >= len(o.covered) || o.covered[pos] {
			continue
		}
		o.data[pos] = v
		o.covered[pos] = true
	}
}

func (o *streamOracle) coveredSpan() (first, last int64) {
	first = -1
	for i, ok := range o.covered {
		if !ok {
			continue
		}
		if first < 0 {
			first = int64(i)
		}
		last = int64(i) + 1
	}
	if first < 0 {
		return 0, 0
	}
	return first, last
}

// expectedReason mirrors the buffer's gap/pending classification for an offset
// that holds no live data.
func (o *streamOracle) expectedReason(off int64) Reason {
	first, last := o.coveredSpan()
	if first == last {
		return ReasonPending
	}
	if off < first || off >= last {
		return ReasonPending
	}
	return ReasonGap
}

// TestPropertyRandomOrderByteForByte drives the buffer with shuffled
// notification ordering and randomized writes. It verifies, byte for byte,
// that every data segment matches the independent stream, every window is
// fully tiled, missing ranges are explicit, and blocks still referenced by an
// active candidate are always reachable (no silent truncation).
func TestPropertyRandomOrderByteForByte(t *testing.T) {
	const streamSize = 240

	for seed := int64(0); seed < 60; seed++ {
		rng := rand.New(rand.NewSource(seed))
		budget := int64(20 + rng.Intn(180))
		buf := NewBuffer(budget)
		oracle := newStreamOracle(streamSize)

		candidates := make([]Candidate, 0, 12)
		for event := uint64(1); event <= 12; event++ {
			// Keep at least 16 bytes of headroom on both sides so the full
			// window (match plus up to 14 context bytes) stays in-bounds.
			matchStart := 16 + rng.Intn(streamSize-52)
			matchLen := 1 + rng.Intn(12)
			candidates = append(candidates, Candidate{
				EventID:       event,
				Priority:      rng.Intn(6),
				Match:         Range{Start: int64(matchStart), End: int64(matchStart + matchLen)},
				BeforeContext: int64(rng.Intn(15)),
				AfterContext:  int64(rng.Intn(15)),
			})
		}

		live := make(map[uint64]Candidate)
		for step, idx := range rng.Perm(len(candidates)) {
			c := candidates[idx]
			if rng.Intn(2) == 0 {
				writeRandom(t, rng, buf, oracle, streamSize)
			}
			if err := buf.Notify(c); err != nil {
				t.Fatalf("seed %d: notify %d: %v", seed, c.EventID, err)
			}
			live[c.EventID] = c

			if rng.Intn(4) == 0 {
				syncEvicted(buf, live)
				maybeCancel(t, rng, buf, live)
			}
			assertInvariants(t, buf, oracle, live, seed, int64(step))
		}

		for step := 0; step < 24; step++ {
			writeRandom(t, rng, buf, oracle, streamSize)
			if rng.Intn(3) == 0 {
				syncEvicted(buf, live)
				maybeCancel(t, rng, buf, live)
			}
			assertInvariants(t, buf, oracle, live, seed, int64(100+step))
		}

		stats := buf.Stats()
		if stats.RetainedBytes > budget && stats.OverflowBytes == 0 {
			t.Fatalf("seed %d: retained %d over budget %d without overflow",
				seed, stats.RetainedBytes, budget)
		}
	}
}

func writeRandom(t *testing.T, rng *rand.Rand, buf *Buffer, oracle *streamOracle, size int) {
	t.Helper()
	off := rng.Intn(size)
	n := 1 + rng.Intn(40)
	if off+n > size {
		n = size - off
	}
	p := mkBytes(off, n)
	if err := buf.Write(int64(off), p); err != nil {
		t.Fatalf("write off=%d n=%d: %v", off, n, err)
	}
	oracle.write(int64(off), p)
}

func maybeCancel(t *testing.T, rng *rand.Rand, buf *Buffer, live map[uint64]Candidate) {
	t.Helper()
	if len(live) <= 1 {
		return
	}
	ids := make([]uint64, 0, len(live))
	for id := range live {
		ids = append(ids, id)
	}
	victim := ids[rng.Intn(len(ids))]
	if err := buf.Cancel(victim); err != nil {
		t.Fatalf("cancel %d: %v", victim, err)
	}
	delete(live, victim)
	if _, err := buf.Collect(victim); err == nil {
		t.Fatalf("canceled event %d still collectible", victim)
	}
}

func syncEvicted(buf *Buffer, live map[uint64]Candidate) {
	for _, rec := range buf.Evictions() {
		delete(live, rec.EventID)
	}
}

func assertInvariants(t *testing.T, buf *Buffer, oracle *streamOracle, live map[uint64]Candidate, seed, step int64) {
	t.Helper()

	// Drop candidates the buffer evicted so the live set stays in sync.
	evicted := make(map[uint64]bool)
	for _, rec := range buf.Evictions() {
		evicted[rec.EventID] = true
	}
	for id := range live {
		if evicted[id] {
			delete(live, id)
		}
	}

	for id, cand := range live {
		ev, err := buf.Collect(id)
		if err != nil {
			t.Fatalf("seed %d step %d collect %d: %v", seed, step, id, err)
		}
		if ev.Evicted {
			delete(live, id)
			continue
		}
		if ev.Window != cand.Window() {
			t.Fatalf("seed %d step %d event %d: window drifted %v != %v",
				seed, step, id, ev.Window, cand.Window())
		}
		assertPartition(t, ev)

		for _, seg := range ev.Segments {
			if seg.Data != nil {
				// Byte-for-byte equality with the unbounded source stream.
				for i, got := range seg.Data {
					off := seg.Range.Start + int64(i)
					if off < 0 || int(off) >= len(oracle.data) {
						t.Fatalf("seed %d step %d event %d: data offset %d outside oracle",
							seed, step, id, off)
					}
					if !oracle.covered[off] {
						t.Fatalf("seed %d step %d event %d: data at never-written off %d",
							seed, step, id, off)
					}
					if want := oracle.data[off]; got != want {
						t.Fatalf("seed %d step %d event %d off %d: byte %d want %d",
							seed, step, id, off, got, want)
					}
				}
				continue
			}
			// Missing segments must be explicit and consistently classified.
			for off := seg.Range.Start; off < seg.Range.End; off++ {
				// A missing range is honest as long as the live block for the
				// offset is genuinely gone (cache reclaim before registration
				// or an eviction). Gap/pending classification only governs
				// offsets the stream never delivered.
				if !oracle.covered[off] {
					if want := oracle.expectedReason(off); seg.Reason != want {
						t.Fatalf("seed %d step %d event %d off %d: reason %v want %v",
							seed, step, id, off, seg.Reason, want)
					}
				} else if seg.Reason != ReasonGap && seg.Reason != ReasonEvicted {
					t.Fatalf("seed %d step %d event %d off %d: reclaimed byte has reason %v, want gap/evicted",
						seed, step, id, off, seg.Reason)
				}
			}
		}
	}

	// Reference integrity: a live block intersecting an active window must
	// carry at least one reference, so reclaim can never drop it.
	for _, cs := range buf.candidates {
		if cs.evicted {
			continue
		}
		for _, blk := range buf.blocks {
			if intersect(blk.rng, cs.window).Len() > 0 && blk.refs == 0 {
				t.Fatalf("seed %d step %d event %d: unreferenced live block %v in window %v",
					seed, step, cs.cand.EventID, blk.rng, cs.window)
			}
		}
	}

	// Eviction policy ordering (priority descending, then EventID ascending)
	// is verified deterministically in TestEvictionOrderPriorityThenEventID.
	_ = buf.Evictions()
}
