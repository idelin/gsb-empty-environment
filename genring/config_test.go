package genring

import "testing"

func TestNewRejectsInvalidConfig(t *testing.T) {
	noop := func(Lease, Tenant, Generation, ReleaseReason) {}
	cases := []Config{
		{Size: 3, TagBits: 2, OnRelease: noop},
		{Size: 2, TagBits: 0, OnRelease: noop},
		{Size: 2, TagBits: 65, OnRelease: noop},
		{Size: 4, TagBits: 3, MaxInflight: 3, OnRelease: noop},
		{Size: 4, TagBits: 3, MaxInflight: -1, OnRelease: noop},
		{Size: 4, TagBits: 3},
	}
	for i, cfg := range cases {
		if _, err := New(cfg); err == nil {
			t.Fatalf("case %d: expected error for %+v", i, cfg)
		}
	}
}

func TestAccessorsAndSlotView(t *testing.T) {
	r, _ := newTestRing(t, 4, 3, 2)
	if r.Epoch() != 0 || r.TagBits() != 3 || r.Isolated() {
		t.Fatal("unexpected base accessor state")
	}
	if r.Draining() {
		t.Fatal("fresh ring should not be draining")
	}

	idx, gen, _ := r.Submit(11, 22)
	view := r.Slot(idx)
	if !view.InFlight() || view.Completed() || view.Gen() != gen ||
		view.Lease() != 11 || view.Tenant() != 22 {
		t.Fatalf("slot view = %+v", view)
	}
	if _, err := r.Complete(idx, r.Tag(gen)); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if r.Slot(idx).InFlight() {
		t.Fatal("slot should no longer be in flight")
	}
}

func TestRolloverRejectsIsolatedAndEpochExhaustion(t *testing.T) {
	r, _ := newTestRing(t, 4, 3, 1)
	r.quarantine()
	if _, err := r.Rollover(); err == nil {
		t.Fatal("rollover on isolated ring must fail")
	}

	r2, _ := newTestRing(t, 4, 3, 1)
	r2.epoch = ^uint32(0)
	if _, err := r2.Reset(); err == nil {
		t.Fatal("reset at max epoch must fail")
	}
	r2.isolated = false
	r2.draining = true
	r2.epoch = ^uint32(0)
	if _, err := r2.Rollover(); err == nil {
		t.Fatal("rollover at max epoch must fail")
	}
}
