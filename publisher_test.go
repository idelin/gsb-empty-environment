package netsec

import (
	"context"
	"errors"
	"testing"
)

func TestBeginRegistersPendingWithoutPublishing(t *testing.T) {
	a := makeUnit(t, "u", 1, 0, "A")
	b := makeUnit(t, "u", 2, 1, "B")
	c := makeUnit(t, "u", 3, 2, "C")
	ma := mustManifest(t, []*UnitVersion{a}, nil)
	mb := mustManifest(t, []*UnitVersion{b}, ma)
	mc := mustManifest(t, []*UnitVersion{c}, mb)

	p, err := NewPublisher(ma)
	if err != nil {
		t.Fatal(err)
	}
	pend, err := p.Begin(mb)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if p.CurrentManifest().Root() != ma.Root() {
		t.Fatal("Begin changed the entry table")
	}
	if pend.State() != PublicationPending {
		t.Fatalf("state = %s, want pending", pend.State())
	}
	if refs := p.ContentStore().Refs(b.ContentRoot()); refs != 1 {
		t.Fatalf("staged content refs = %d, want 1", refs)
	}
	if _, err := p.Begin(mc); !errors.Is(err, ErrPublicationInProgress) {
		t.Fatalf("second Begin error = %v", err)
	}
	if err := p.Abort(pend, errors.New("cancelled")); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if p.Pending() != nil {
		t.Fatal("pending publication was not cleared")
	}
	if p.CurrentManifest().Root() != ma.Root() {
		t.Fatal("Abort changed the entry table")
	}
	if p.ContentStore().Has(b.ContentRoot()) {
		t.Fatal("aborted staged content was not reclaimed")
	}
	if !p.ContentStore().Has(a.ContentRoot()) {
		t.Fatal("live content was reclaimed")
	}
}

func TestBarrierRejectsUnpinnedThreadInReplacedRange(t *testing.T) {
	a := makeUnit(t, "u", 1, 0, "A")
	b := makeUnit(t, "u", 2, 1, "B")
	ma := mustManifest(t, []*UnitVersion{a}, nil)
	mb := mustManifest(t, []*UnitVersion{b}, ma)

	p, _ := NewPublisher(ma)
	thread := newFakeThread("t1", BarrierReport{PC: 0x20, ReturnPC: 0x30, Parked: true})
	if err := p.RegisterThread(thread); err != nil {
		t.Fatal(err)
	}
	_, err := p.Publish(context.Background(), mb)
	if !errors.Is(err, ErrBlockedInPatch) {
		t.Fatalf("Publish error = %v, want ErrBlockedInPatch", err)
	}
	if p.CurrentManifest().Root() != ma.Root() {
		t.Fatal("failed barrier changed the entry table")
	}
	if p.ContentStore().Has(b.ContentRoot()) {
		t.Fatal("failed publication left orphan content reachable")
	}
	if p.Pending() != nil {
		t.Fatal("failed publication remained pending")
	}
	if thread.releaseCount() != 1 {
		t.Fatalf("parked thread releases after abort = %d, want 1", thread.releaseCount())
	}
}

func TestBarrierAcceptsThreadPinnedToOldGeneration(t *testing.T) {
	a := makeUnit(t, "u", 1, 0, "A")
	b := makeUnit(t, "u", 2, 1, "B")
	ma := mustManifest(t, []*UnitVersion{a}, nil)
	mb := mustManifest(t, []*UnitVersion{b}, ma)

	p, _ := NewPublisher(ma)
	oldLease, err := p.PinFrame(ma)
	if err != nil {
		t.Fatal(err)
	}
	thread := newFakeThread("t1", BarrierReport{
		PC:       0x20,
		ReturnPC: 0x30,
		Parked:   true,
		Lease:    oldLease,
	})
	if err := p.RegisterThread(thread); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Publish(context.Background(), mb); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if p.CurrentManifest().Root() != mb.Root() {
		t.Fatal("entry table was not swapped")
	}
	if thread.releaseCount() != 1 {
		t.Fatalf("parked thread releases = %d, want 1", thread.releaseCount())
	}
	if err := p.TryReclaim(ma); !errors.Is(err, ErrLeaseOutstanding) {
		t.Fatalf("reclaim with old frame lease = %v, want ErrLeaseOutstanding", err)
	}
	oldLease.Release()
	if err := p.TryReclaim(ma); err != nil {
		t.Fatalf("reclaim after lease release: %v", err)
	}
	if p.ContentStore().Has(a.ContentRoot()) {
		t.Fatal("reclaimed generation content still present")
	}
	if _, err := p.PinFrame(ma); !errors.Is(err, ErrManifestReclaimed) {
		t.Fatalf("late pin error = %v, want ErrManifestReclaimed", err)
	}
}

func TestBarrierNeedsNoLeaseOutsideReplacedRanges(t *testing.T) {
	a := makeUnit(t, "u", 1, 0, "A")
	b := makeUnit(t, "u", 2, 1, "B")
	ma := mustManifest(t, []*UnitVersion{a}, nil)
	mb := mustManifest(t, []*UnitVersion{b}, ma)

	p, _ := NewPublisher(ma)
	thread := newFakeThread("t1", BarrierReport{PC: 0x100, ReturnPC: 0x120})
	_ = p.RegisterThread(thread)
	if _, err := p.Publish(context.Background(), mb); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if thread.releaseCount() != 0 {
		t.Fatal("an unparked thread was released")
	}
}

func TestBarrierChecksOnlyUnitsReplacedByNextManifest(t *testing.T) {
	specA1 := makeUnitSpec("a", 1, 0, "A")
	specA1.PatchRanges = []Range{{Start: 0, End: 0x30}}
	a1, err := NewUnitVersion(specA1, Validators{})
	if err != nil {
		t.Fatal(err)
	}
	specA2 := makeUnitSpec("a", 2, 1, "B")
	specA2.PatchRanges = []Range{{Start: 0, End: 0x30}}
	a2, err := NewUnitVersion(specA2, Validators{})
	if err != nil {
		t.Fatal(err)
	}
	specB := makeUnitSpec("b", 1, 0, "C")
	specB.PatchRanges = []Range{{Start: 0x100, End: 0x130}}
	b, err := NewUnitVersion(specB, Validators{})
	if err != nil {
		t.Fatal(err)
	}
	ma := mustManifest(t, []*UnitVersion{a1, b}, nil)
	mb := mustManifest(t, []*UnitVersion{a2, b}, ma)

	p, _ := NewPublisher(ma)
	// b is unchanged by the next manifest. Its patch range must not force a
	// lease even though unit a is being replaced.
	thread := newFakeThread("t1", BarrierReport{PC: 0x110, ReturnPC: 0x111})
	_ = p.RegisterThread(thread)
	if _, err := p.Publish(context.Background(), mb); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

func TestBarrierProbeFailureAbortsPublication(t *testing.T) {
	a := makeUnit(t, "u", 1, 0, "A")
	b := makeUnit(t, "u", 2, 1, "B")
	ma := mustManifest(t, []*UnitVersion{a}, nil)
	mb := mustManifest(t, []*UnitVersion{b}, ma)

	p, _ := NewPublisher(ma)
	thread := newFakeThread("t1", BarrierReport{})
	thread.probeErr = errors.New("thread did not stop")
	_ = p.RegisterThread(thread)
	if _, err := p.Publish(context.Background(), mb); err == nil {
		t.Fatal("Publish unexpectedly succeeded")
	}
	if p.CurrentManifest().Root() != ma.Root() {
		t.Fatal("probe failure changed the entry table")
	}
	if p.ContentStore().Has(b.ContentRoot()) {
		t.Fatal("probe failure did not reclaim staged content")
	}
}

func TestNewThreadJoiningDuringBarrierIsProbed(t *testing.T) {
	a := makeUnit(t, "u", 1, 0, "A")
	b := makeUnit(t, "u", 2, 1, "B")
	ma := mustManifest(t, []*UnitVersion{a}, nil)
	mb := mustManifest(t, []*UnitVersion{b}, ma)

	p, _ := NewPublisher(ma)
	t1 := newFakeThread("t1", BarrierReport{})
	t2 := newFakeThread("t2", BarrierReport{})
	t1.onProbe = func() {
		if err := p.RegisterThread(t2); err != nil {
			t.Errorf("RegisterThread(t2): %v", err)
		}
	}
	if err := p.RegisterThread(t1); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Publish(context.Background(), mb); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if t2.probeCount() != 1 {
		t.Fatalf("joined thread probes = %d, want 1", t2.probeCount())
	}
}

func TestReplacedThreadWithSameIDIsProbedAgain(t *testing.T) {
	a := makeUnit(t, "u", 1, 0, "A")
	b := makeUnit(t, "u", 2, 1, "B")
	ma := mustManifest(t, []*UnitVersion{a}, nil)
	mb := mustManifest(t, []*UnitVersion{b}, ma)

	p, _ := NewPublisher(ma)
	oldThread := newFakeThread("t1", BarrierReport{})
	newThread := newFakeThread("t1", BarrierReport{})
	oldThread.onProbe = func() {
		if err := p.UnregisterThread("t1"); err != nil {
			t.Errorf("UnregisterThread: %v", err)
			return
		}
		if err := p.RegisterThread(newThread); err != nil {
			t.Errorf("RegisterThread replacement: %v", err)
		}
	}
	if err := p.RegisterThread(oldThread); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Publish(context.Background(), mb); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if newThread.probeCount() != 1 {
		t.Fatalf("replacement thread probes = %d, want 1", newThread.probeCount())
	}
}

func TestCommitRequiresFreshBarrierAfterThreadJoin(t *testing.T) {
	a := makeUnit(t, "u", 1, 0, "A")
	b := makeUnit(t, "u", 2, 1, "B")
	ma := mustManifest(t, []*UnitVersion{a}, nil)
	mb := mustManifest(t, []*UnitVersion{b}, ma)

	p, _ := NewPublisher(ma)
	t1 := newFakeThread("t1", BarrierReport{})
	if err := p.RegisterThread(t1); err != nil {
		t.Fatal(err)
	}
	pend, err := p.Begin(mb)
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Barrier(context.Background(), pend)
	if err != nil {
		t.Fatal(err)
	}
	t2 := newFakeThread("t2", BarrierReport{})
	if err := p.RegisterThread(t2); err != nil {
		t.Fatal(err)
	}
	if err := p.Commit(pend, res); !errors.Is(err, ErrThreadsChanged) {
		t.Fatalf("Commit error = %v, want ErrThreadsChanged", err)
	}
	if p.CurrentManifest().Root() != ma.Root() {
		t.Fatal("thread change committed anyway")
	}
	res, err = p.Barrier(context.Background(), pend)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Commit(pend, res); err != nil {
		t.Fatalf("second Commit: %v", err)
	}
	if t2.probeCount() != 1 {
		t.Fatalf("new thread probes = %d, want 1", t2.probeCount())
	}
}

func TestCommitRechecksLeasePinsAfterBarrier(t *testing.T) {
	a := makeUnit(t, "u", 1, 0, "A")
	b := makeUnit(t, "u", 2, 1, "B")
	ma := mustManifest(t, []*UnitVersion{a}, nil)
	mb := mustManifest(t, []*UnitVersion{b}, ma)

	p, _ := NewPublisher(ma)
	lease, err := p.PinFrame(ma)
	if err != nil {
		t.Fatal(err)
	}
	thread := newFakeThread("t1", BarrierReport{
		PC:       0x20,
		ReturnPC: 0x30,
		Parked:   true,
		Lease:    lease,
	})
	if err := p.RegisterThread(thread); err != nil {
		t.Fatal(err)
	}
	pend, err := p.Begin(mb)
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Barrier(context.Background(), pend)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	if err := p.Commit(pend, res); !errors.Is(err, ErrBlockedInPatch) {
		t.Fatalf("Commit with released lease = %v, want ErrBlockedInPatch", err)
	}
	if p.CurrentManifest().Root() != ma.Root() {
		t.Fatal("stale lease committed anyway")
	}
	if err := p.Abort(pend, nil); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if thread.releaseCount() != 1 {
		t.Fatalf("parked thread releases = %d, want 1", thread.releaseCount())
	}
}

func TestAllLeaseClassesMustDrainBeforeReclaim(t *testing.T) {
	a := makeUnit(t, "u", 1, 0, "A")
	b := makeUnit(t, "u", 2, 1, "B")
	ma := mustManifest(t, []*UnitVersion{a}, nil)
	mb := mustManifest(t, []*UnitVersion{b}, ma)

	p, _ := NewPublisher(ma)
	if _, err := p.Publish(context.Background(), mb); err != nil {
		t.Fatal(err)
	}
	frame, _ := p.PinFrame(ma)
	callback, _ := p.PinCallback(ma)
	sampler, _ := p.PinSampler(ma)
	if got := p.LeaseCounts(ma); got != (LeaseCounts{Frames: 1, Callbacks: 1, Samplers: 1}) {
		t.Fatalf("lease counts = %+v", got)
	}
	for _, tc := range []struct {
		name string
		do   func()
	}{
		{"frame", frame.Release},
		{"callback", callback.Release},
		{"sampler", sampler.Release},
	} {
		if err := p.TryReclaim(ma); !errors.Is(err, ErrLeaseOutstanding) {
			t.Fatalf("reclaim before draining %s = %v", tc.name, err)
		}
		tc.do()
	}
	if got := p.LeaseCounts(ma); got.Total() != 0 {
		t.Fatalf("leases remain after release: %+v", got)
	}
	if err := p.TryReclaim(ma); err != nil {
		t.Fatalf("reclaim after draining: %v", err)
	}
	if p.ContentStore().Has(a.ContentRoot()) {
		t.Fatal("reclaimed unit content still present")
	}
	if !p.ContentStore().Has(b.ContentRoot()) {
		t.Fatal("current unit content was reclaimed")
	}
}

func TestCrashRecoveryKeepsOnlyCompleteLiveManifest(t *testing.T) {
	a := makeUnit(t, "u", 1, 0, "A")
	b := makeUnit(t, "u", 2, 1, "B")
	c := makeUnit(t, "u", 3, 2, "C")
	ma := mustManifest(t, []*UnitVersion{a}, nil)
	mb := mustManifest(t, []*UnitVersion{b}, ma)
	mc := mustManifest(t, []*UnitVersion{c}, mb)

	p, _ := NewPublisher(ma)
	pend, err := p.Begin(mb)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Recover(ma, Validators{}); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if p.CurrentManifest().Root() != ma.Root() {
		t.Fatal("recovery did not restore the complete live manifest")
	}
	if p.Pending() != nil {
		t.Fatal("recovery kept a pending publication")
	}
	if pend.State() != PublicationAborted {
		t.Fatalf("interrupted publication state = %s, want aborted", pend.State())
	}
	if p.ContentStore().Len() != 1 || !p.ContentStore().Has(a.ContentRoot()) {
		t.Fatalf("store after recovery = len %d, a=%t", p.ContentStore().Len(), p.ContentStore().Has(a.ContentRoot()))
	}
	if p.ContentStore().Has(b.ContentRoot()) {
		t.Fatal("orphaned staged content survived recovery")
	}
	if _, err := p.Begin(mc); err != nil {
		t.Fatalf("publisher unusable after recovery: %v", err)
	}
}

func TestRecoveryRejectsTamperedManifest(t *testing.T) {
	a := makeUnit(t, "u", 1, 0, "A")
	b := makeUnit(t, "u", 2, 1, "B")
	ma := mustManifest(t, []*UnitVersion{a}, nil)
	mb := mustManifest(t, []*UnitVersion{b}, nil)
	p, _ := NewPublisher(ma)
	mb.root[0] ^= 0xff
	if err := p.Recover(mb, Validators{}); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Recover(tampered) = %v, want ErrDigestMismatch", err)
	}
	if p.CurrentManifest().Root() != ma.Root() {
		t.Fatal("failed recovery changed the current manifest")
	}
}
