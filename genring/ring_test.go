package genring

import "testing"

type releaseEvent struct {
	lease  Lease
	tenant Tenant
	gen    Generation
	reason ReleaseReason
}

type recorder struct {
	events []releaseEvent
	counts map[Lease]int
}

func newRecorder() *recorder {
	return &recorder{counts: map[Lease]int{}}
}

func (rc *recorder) onRelease(lease Lease, tenant Tenant, gen Generation, reason ReleaseReason) {
	rc.events = append(rc.events, releaseEvent{lease, tenant, gen, reason})
	rc.counts[lease]++
}

func newTestRing(t *testing.T, size, tagBits, maxInflight int) (*Ring, *recorder) {
	t.Helper()
	rc := newRecorder()
	r, err := New(Config{
		Size:        size,
		TagBits:     tagBits,
		MaxInflight: maxInflight,
		OnRelease:   rc.onRelease,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r, rc
}

func TestSubmitAndCompleteBasic(t *testing.T) {
	r, rc := newTestRing(t, 4, 3, 2)

	idx, gen, err := r.Submit(10, 20)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if idx != 0 || gen.Epoch() != 0 || gen.Sequence() != 1 {
		t.Fatalf("unexpected submission idx=%d gen=%+v", idx, gen)
	}
	if r.Inflight() != 1 {
		t.Fatalf("inflight = %d, want 1", r.Inflight())
	}

	res, err := r.Complete(idx, r.Tag(gen))
	if err != nil || !res.Released || res.Slot != idx || res.Generation != gen {
		t.Fatalf("Complete = %+v, %v", res, err)
	}
	if len(rc.events) != 1 || rc.events[0].lease != 10 || rc.events[0].tenant != 20 ||
		rc.events[0].reason != ReleaseCompleted {
		t.Fatalf("release event = %+v", rc.events)
	}
	if r.Len() != 0 {
		t.Fatalf("ring not reclaimed, len=%d", r.Len())
	}
}

func TestOutOfOrderCompletions(t *testing.T) {
	r, rc := newTestRing(t, 4, 3, 2)

	type sub struct {
		idx int
		gen Generation
	}
	subs := make([]sub, 2)
	for i := range subs {
		idx, gen, err := r.Submit(Lease(100+i), Tenant(7))
		if err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
		subs[i] = sub{idx, gen}
	}
	if r.FreeSlots() != 0 {
		t.Fatalf("free slots = %d, want 0", r.FreeSlots())
	}

	// Complete the newer entry first; the older tail slot must stay occupied.
	if _, err := r.Complete(subs[1].idx, r.Tag(subs[1].gen)); err != nil {
		t.Fatalf("complete newer: %v", err)
	}
	if r.Stats().Completed != 1 || r.Len() != 2 {
		t.Fatalf("out-of-order slot should stay occupied: %+v", r.Stats())
	}
	if _, err := r.Complete(subs[0].idx, r.Tag(subs[0].gen)); err != nil {
		t.Fatalf("complete older: %v", err)
	}
	if r.Len() != 0 || r.Inflight() != 0 {
		t.Fatalf("ring not drained: len=%d inflight=%d", r.Len(), r.Inflight())
	}

	// Both slots are reclaimed; the next submission reuses an index but gets
	// a fresh generation while leases stay unique.
	idx, gen, err := r.Submit(102, 7)
	if err != nil {
		t.Fatalf("reuse Submit: %v", err)
	}
	if gen.Sequence() != 3 {
		t.Fatalf("reuse gen=%+v, want sequence 3", gen)
	}
	if _, err := r.Complete(idx, r.Tag(gen)); err != nil {
		t.Fatalf("reuse complete: %v", err)
	}

	for lease, n := range rc.counts {
		if n != 1 {
			t.Fatalf("lease %d released %d times", lease, n)
		}
	}
}
