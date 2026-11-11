package genring

import (
	"errors"
	"testing"
)

func TestSequenceExhaustionDrainAndRollover(t *testing.T) {
	r, rc := newTestRing(t, 4, 3, 1)
	r.SetSequenceCeiling(3)

	// Three bounded-depth submissions: each drains before the next one, but
	// the sequence space still runs out and blocks further submissions.
	var gens []Generation
	for i := 0; i < 3; i++ {
		idx, gen, err := r.Submit(Lease(i+1), Tenant(i+1))
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
		gens = append(gens, gen)
		if _, err := r.Complete(idx, r.Tag(gen)); err != nil {
			t.Fatalf("complete %d: %v", i, err)
		}
	}

	if !r.Draining() {
		t.Fatal("ring should be draining after hitting the sequence ceiling")
	}
	if _, _, err := r.Submit(99, 99); !errors.Is(err, ErrSequenceExhausted) {
		t.Fatalf("submit after drain err = %v, want ErrSequenceExhausted", err)
	}

	epoch, err := r.Rollover()
	if err != nil || epoch != 1 {
		t.Fatalf("rollover = %d, %v", epoch, err)
	}

	idx, gen, err := r.Submit(40, 40)
	if err != nil {
		t.Fatalf("post-rollover submit: %v", err)
	}
	if gen.Epoch() != 1 || gen.Sequence() != 1 || idx != 0 {
		t.Fatalf("post-rollover generation = %+v idx=%d", gen, idx)
	}
	if _, err := r.Complete(idx, r.Tag(gen)); err != nil {
		t.Fatalf("post-rollover complete: %v", err)
	}

	for lease, n := range rc.counts {
		if n != 1 {
			t.Fatalf("lease %d released %d times", lease, n)
		}
	}

	// Also exercise "drain before rollover" while a lease is still in flight.
	r.SetSequenceCeiling(2)
	idx, gen, err = r.Submit(5, 5)
	if err != nil {
		t.Fatalf("submit before second exhaustion: %v", err)
	}
	if _, _, err := r.Submit(6, 6); !errors.Is(err, ErrDraining) {
		t.Fatalf("second draining err = %v", err)
	}
	if _, err := r.Rollover(); !errors.Is(err, ErrInFlight) {
		t.Fatalf("rollover while in flight err = %v", err)
	}
	if _, err := r.Complete(idx, r.Tag(gen)); err != nil {
		t.Fatalf("drain in-flight: %v", err)
	}
	if ep, err := r.Rollover(); err != nil || ep != 2 {
		t.Fatalf("second rollover = %d, %v", ep, err)
	}
}

func TestRolloverOnlyWhenExhausted(t *testing.T) {
	r, _ := newTestRing(t, 4, 3, 2)
	if _, err := r.Rollover(); !errors.Is(err, ErrNotExhausted) {
		t.Fatalf("unexpected rollover err = %v", err)
	}

	r.SetSequenceCeiling(1)
	idx, gen, err := r.Submit(1, 1)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, _, err := r.Submit(2, 2); !errors.Is(err, ErrDraining) {
		t.Fatalf("draining err = %v", err)
	}
	if _, err := r.Complete(idx, r.Tag(gen)); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if epoch, err := r.Rollover(); err != nil || epoch != 1 {
		t.Fatalf("rollover = %d, %v", epoch, err)
	}
}
