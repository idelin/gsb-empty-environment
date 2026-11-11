package genring

import (
	"errors"
	"testing"
)

func TestAmbiguousTagQuarantinesAndRecoversViaReset(t *testing.T) {
	// Size 8, half ring in flight (4), tag width 1. A single in-flight slot
	// is always unambiguous; reusing a retired slot while another slot stays
	// in flight can produce two in-flight generations with the same tag,
	// which is genuinely ambiguous and must quarantine the queue.
	r, rc := newTestRing(t, 8, 1, 4)

	// gen 1 (tag 1) and gen 2 (tag 0): two adjacent, uniquely tagged slots.
	idx5, gen5, err := r.Submit(5, 2)
	if err != nil {
		t.Fatalf("gen1 submit: %v", err)
	}
	_, gen6, err := r.Submit(6, 2)
	if err != nil {
		t.Fatalf("gen2 submit: %v", err)
	}
	if _, err := r.Complete(idx5, r.Tag(gen5)); err != nil {
		t.Fatalf("complete gen1: %v", err)
	}
	idx7, gen7, err := r.Submit(7, 3)
	if err != nil {
		t.Fatalf("gen3 submit: %v", err)
	}
	if _, err := r.Complete(idx7, r.Tag(gen7)); err != nil {
		t.Fatalf("complete gen3: %v", err)
	}
	idx8, gen8, err := r.Submit(8, 3)
	if err != nil {
		t.Fatalf("gen4 submit: %v", err)
	}
	if r.Tag(gen6) != 0 || r.Tag(gen8) != 0 {
		t.Fatalf("setup tags gen6=%d gen8=%d", r.Tag(gen6), r.Tag(gen8))
	}

	// Feed the colliding tag against an unrelated, non-in-flight slot index.
	// Two in-flight generations match and neither can be assumed, so the ring
	// must quarantine rather than guess (and must not pick the newest).
	res, err := r.Complete(idx5, r.Tag(gen8))
	if !errors.Is(err, ErrAmbiguousGeneration) || !res.Quarantined {
		t.Fatalf("ambiguous completion = %+v, %v", res, err)
	}
	if !r.Isolated() {
		t.Fatal("ring not isolated after ambiguity")
	}

	// Quarantine hands every occupied lease back exactly once. Lease 7
	// completed but stayed occupied behind in-flight gen 6, so it is
	// recovered here rather than through normal reclaim.
	for _, lease := range []Lease{6, 7, 8} {
		if rc.counts[lease] != 1 {
			t.Fatalf("lease %d count = %d, want 1", lease, rc.counts[lease])
		}
	}
	if got := rc.events[len(rc.events)-1].reason; got != ReleaseQuarantine {
		t.Fatalf("last reason = %v, want quarantine", got)
	}

	if _, _, err := r.Submit(9, 9); !errors.Is(err, ErrIsolated) {
		t.Fatalf("submit on isolated ring err = %v", err)
	}
	if _, err := r.Complete(idx8, 0); !errors.Is(err, ErrIsolated) {
		t.Fatalf("complete on isolated ring err = %v", err)
	}

	if _, err := r.Reset(); err != nil {
		t.Fatalf("reset after quarantine: %v", err)
	}
	if r.Isolated() {
		t.Fatal("reset must clear isolation")
	}
	newIdx, newGen, err := r.Submit(20, 20)
	if err != nil {
		t.Fatalf("submit after reset: %v", err)
	}
	if _, err := r.Complete(newIdx, r.Tag(newGen)); err != nil {
		t.Fatalf("complete after reset: %v", err)
	}
	if rc.counts[20] != 1 {
		t.Fatalf("lease 20 count = %d", rc.counts[20])
	}
}

func TestAmbiguousEvenWhenSlotIndexMatches(t *testing.T) {
	// Delivering the colliding tag at one of the actual owner slot indices
	// must still quarantine: a matching (slot, tag) pair is not sufficient
	// when another in-flight generation shares the same truncated tag.
	r, _ := newTestRing(t, 8, 1, 4)

	idx1, gen1, _ := r.Submit(1, 1)
	idx2, gen2, _ := r.Submit(2, 2)
	if _, err := r.Complete(idx1, r.Tag(gen1)); err != nil {
		t.Fatalf("complete gen1: %v", err)
	}
	idx3, gen3, _ := r.Submit(3, 3)
	if _, err := r.Complete(idx3, r.Tag(gen3)); err != nil {
		t.Fatalf("complete gen3: %v", err)
	}
	_, gen4, _ := r.Submit(4, 4)

	// gen2 and gen4 must now collide on tag 0 across the in-flight window.
	if r.Tag(gen2) != r.Tag(gen4) {
		t.Fatalf("setup failed: tags %d vs %d", r.Tag(gen2), r.Tag(gen4))
	}
	// Report against idx2 (a real owner of the tag): ambiguity still wins.
	res, err := r.Complete(idx2, r.Tag(gen4))
	if !errors.Is(err, ErrAmbiguousGeneration) || !res.Quarantined {
		t.Fatalf("matching-index ambiguous completion = %+v, %v", res, err)
	}
	if !r.Isolated() {
		t.Fatal("ring must be isolated")
	}
}

func TestSafeTagBitsNeverAmbiguousInWindow(t *testing.T) {
	if got, want := SafeTagBits(4), 3; got != want {
		t.Fatalf("SafeTagBits(4) = %d, want %d", got, want)
	}
	if got, want := SafeTagBits(8), 4; got != want {
		t.Fatalf("SafeTagBits(8) = %d, want %d", got, want)
	}

	r, rc := newTestRing(t, 4, SafeTagBits(4), 2)
	// Many reuse cycles with out-of-order completions: every tag inside the
	// half-ring window must remain uniquely attributable.
	for cycle := 0; cycle < 64; cycle++ {
		var pending []int
		var gens []Generation
		for r.FreeSlots() > 0 {
			idx, gen, err := r.Submit(Lease(1000+cycle*8+len(gens)), Tenant(cycle%3))
			if err != nil {
				t.Fatalf("submit: %v", err)
			}
			pending = append(pending, idx)
			gens = append(gens, gen)
		}
		if cycle%2 == 0 {
			for i := len(pending) - 1; i >= 0; i-- {
				if _, err := r.Complete(pending[i], r.Tag(gens[i])); err != nil {
					t.Fatalf("cycle %d complete %d: %v", cycle, i, err)
				}
			}
		} else {
			for i := range pending {
				if _, err := r.Complete(pending[i], r.Tag(gens[i])); err != nil {
					t.Fatalf("cycle %d complete %d: %v", cycle, i, err)
				}
			}
		}
		if r.Isolated() {
			t.Fatalf("cycle %d: safe tag width isolated the ring", cycle)
		}
	}
	for lease, n := range rc.counts {
		if n != 1 {
			t.Fatalf("lease %d released %d times", lease, n)
		}
	}
}
