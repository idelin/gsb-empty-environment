package genring

import (
	"errors"
	"testing"
)

func TestResetReleasesInflightAndRejectsLate(t *testing.T) {
	r, rc := newTestRing(t, 4, 3, 2)
	idx, gen, _ := r.Submit(5, 50)
	idx2, gen2, _ := r.Submit(6, 60)

	epoch, err := r.Reset()
	if err != nil || epoch != 1 {
		t.Fatalf("Reset = %d, %v", epoch, err)
	}
	if r.Inflight() != 0 || r.Len() != 0 || r.Isolated() {
		t.Fatalf("ring not clean after reset: %+v", r.Stats())
	}
	if rc.counts[5] != 1 || rc.counts[6] != 1 {
		t.Fatalf("reset release counts = %+v", rc.counts)
	}
	for _, ev := range rc.events {
		if ev.reason != ReleaseReset {
			t.Fatalf("unexpected reason %v", ev.reason)
		}
	}

	// Pre-reset completion entries must not release anything post-reset.
	res, err := r.Complete(idx, r.Tag(gen))
	if !errors.Is(err, ErrStaleCompletion) || res.Released {
		t.Fatalf("pre-reset CQE = %+v, %v", res, err)
	}
	res, err = r.Complete(idx2, r.Tag(gen2))
	if !errors.Is(err, ErrStaleCompletion) || res.Released {
		t.Fatalf("pre-reset CQE 2 = %+v, %v", res, err)
	}

	// Service resumes in the new epoch.
	newIdx, newGen, err := r.Submit(7, 70)
	if err != nil {
		t.Fatalf("post-reset submit: %v", err)
	}
	if newGen.Epoch() != 1 {
		t.Fatalf("post-reset epoch = %d", newGen.Epoch())
	}
	if _, err := r.Complete(newIdx, r.Tag(newGen)); err != nil {
		t.Fatalf("post-reset complete: %v", err)
	}
	if rc.counts[7] != 1 {
		t.Fatalf("new lease release = %d", rc.counts[7])
	}
}

func TestResetRecoversQuarantine(t *testing.T) {
	r, rc := newTestRing(t, 4, 3, 2)
	r.Submit(1, 1)
	r.quarantine()
	if !r.Isolated() {
		t.Fatal("expected isolation")
	}
	if rc.counts[1] != 1 || rc.events[0].reason != ReleaseQuarantine {
		t.Fatalf("quarantine event = %+v", rc.events)
	}

	epoch, err := r.Reset()
	if err != nil || epoch != 1 {
		t.Fatalf("reset = %d %v", epoch, err)
	}
	if r.Isolated() || r.Len() != 0 {
		t.Fatalf("ring not recovered: %+v", r.Stats())
	}
	idx, gen, err := r.Submit(2, 2)
	if err != nil {
		t.Fatalf("submit after reset: %v", err)
	}
	if _, err := r.Complete(idx, r.Tag(gen)); err != nil {
		t.Fatalf("complete after reset: %v", err)
	}
	if rc.counts[2] != 1 {
		t.Fatalf("lease 2 count = %d", rc.counts[2])
	}
}

func TestLateCompletionWithSafeTagAfterReuse(t *testing.T) {
	// SafeTagBits guarantees a retired slot's old tag cannot match any new
	// in-flight generation, so late entries are rejected as stale without
	// touching the new tenant's lease.
	r, rc := newTestRing(t, 4, SafeTagBits(4), 2)
	idx0, gen0, _ := r.Submit(1, 1)
	idx1, gen1, _ := r.Submit(2, 2)
	if _, err := r.Complete(idx0, r.Tag(gen0)); err != nil {
		t.Fatalf("complete 0: %v", err)
	}
	if _, err := r.Complete(idx1, r.Tag(gen1)); err != nil {
		t.Fatalf("complete 1: %v", err)
	}

	newIdx, newGen, _ := r.Submit(3, 3)
	// Replay both old CQEs; they must be stale and release nothing.
	for i, gen := range []Generation{gen0, gen1} {
		res, err := r.Complete(i, r.Tag(gen))
		if !errors.Is(err, ErrStaleCompletion) || !res.Stale || res.Released {
			t.Fatalf("late CQE %d = %+v, %v", i, res, err)
		}
	}
	if _, err := r.Complete(newIdx, r.Tag(newGen)); err != nil {
		t.Fatalf("new completion: %v", err)
	}
	if rc.counts[1] != 1 || rc.counts[2] != 1 || rc.counts[3] != 1 {
		t.Fatalf("release counts = %+v", rc.counts)
	}
}
