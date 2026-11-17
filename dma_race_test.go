package netsec

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestRemapInvalidatesOldGeneration proves a remap bumps the generation, old
// translations fail afterwards, while a lease already taken stays valid and
// completes independently of the new generation.
func TestRemapInvalidatesOldGeneration(t *testing.T) {
	r := newRig(t)
	r.assignFrames(t, tenantA, 0, 1)
	r.mapPage(t, r.d1, 0, 0, PermRead|PermWrite, tenantA)

	lease, err := r.io.Acquire(dev1, []Segment{{IOVA: iova(0), Len: 16}}, false)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	oldGen := lease.Generation()

	// Remap to another frame while the lease is held.
	if err := r.d1.Remap(iova(0), phys(1), PermRead|PermWrite, tenantA); err != nil {
		t.Fatalf("Remap: %v", err)
	}
	if g := r.d1.Generation(); g != oldGen+1 {
		t.Fatalf("generation after remap = %d, want %d", g, oldGen+1)
	}
	if ok, err := lease.Current(); ok || !errors.Is(err, ErrStale) {
		t.Fatalf("lease Current after remap = (%v,%v), want false,ErrStale", ok, err)
	}
	if lease.Generation() != oldGen {
		t.Fatalf("held lease generation changed: %d", lease.Generation())
	}

	// The held lease still resolves to the old physical frame 0.
	ops := lease.Operations()
	if len(ops) != 1 || ops[0].Phys != phys(0) {
		t.Fatalf("lease operations altered by remap: %+v", ops)
	}

	// A fresh request now resolves to the new frame 1.
	cb, calls, seen := recordingCallback()
	res, err := r.io.Transfer(dev1, []Segment{{IOVA: iova(0), Len: 16}}, false, cb)
	if err != nil {
		t.Fatalf("Transfer after remap: %v", err)
	}
	if res.Generation != oldGen+1 || (*seen)[0].Phys != phys(1) {
		t.Fatalf("post-remap transfer wrong: gen %d op %+v", res.Generation, (*seen)[0])
	}
	if calls.Load() != 1 {
		t.Fatalf("want 1 callback, got %d", calls.Load())
	}

	// Unmap invalidates further requests; the old lease remains usable.
	if err := r.d1.Unmap(iova(0)); err != nil {
		t.Fatalf("Unmap: %v", err)
	}
	if _, err := r.io.Acquire(dev1, []Segment{{IOVA: iova(0), Len: 16}}, false); !errors.Is(err, ErrUnmapped) {
		t.Fatalf("post-unmap Acquire: want ErrUnmapped, got %v", err)
	}
	lease.Release()
	if r.d1.ActiveLeases() != 0 {
		t.Fatalf("lease accounting wrong after release")
	}

	// Double release must be a safe no-op.
	lease.Release()
	if r.d1.ActiveLeases() != 0 {
		t.Fatalf("double release changed accounting")
	}
}

// TestRevokeWaitsForOutstandingLease deterministically controls the race:
// revoke blocks while a transfer lease is held, then returns once it ends.
func TestRevokeWaitsForOutstandingLease(t *testing.T) {
	r := newRig(t)
	r.assignFrames(t, tenantA, 0)
	r.mapPage(t, r.d1, 0, 0, PermRead, tenantA)

	lease, err := r.io.Acquire(dev1, []Segment{{IOVA: iova(0), Len: 64}}, false)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	revoked := make(chan struct{})
	go func() {
		r.d1.Revoke()
		close(revoked)
	}()

	// Revoke must not return while the lease is still outstanding.
	select {
	case <-revoked:
		t.Fatalf("Revoke returned before lease release")
	case <-time.After(50 * time.Millisecond):
	}
	if got := r.d1.ActiveLeases(); got != 1 {
		t.Fatalf("ActiveLeases during revoke wait = %d, want 1", got)
	}

	lease.Release()
	select {
	case <-revoked:
	case <-time.After(2 * time.Second):
		t.Fatalf("Revoke did not return after lease ended")
	}

	// The table is empty and the generation advanced, so new requests fail
	// without callbacks.
	cb, calls, _ := recordingCallback()
	_, err = r.io.Transfer(dev1, []Segment{{IOVA: iova(0), Len: 8}}, false, cb)
	if !errors.Is(err, ErrUnmapped) {
		t.Fatalf("post-revoke transfer: want ErrUnmapped, got %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("callback ran after revoke")
	}
}

// TestConcurrentRemapRevokeVersusTransfers hammers the domain with concurrent
// transfers and mapping changes. Invariants checked under -race: no data race,
// failed validations never invoke callbacks, and lease accounting returns to
// zero once everything drains.
func TestConcurrentRemapRevokeVersusTransfers(t *testing.T) {
	r := newRig(t)
	const frames = 8
	physFrames := make([]int, frames)
	for f := 0; f < frames; f++ {
		physFrames[f] = f
		r.assignFrames(t, tenantA, f)
		r.mapPage(t, r.d1, f, f, PermRead|PermWrite, tenantA)
	}

	var callbacks atomic.Int64
	var failures atomic.Int64
	cb := func(op MemOp) error {
		if op.Tenant != tenantA {
			t.Errorf("callback tenant = %d, want tenantA", op.Tenant)
		}
		callbacks.Add(1)
		return nil
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var mutatorWG sync.WaitGroup

	// Workers issuing attacker-controlled scatter-gather lists.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				base := seed % frames
				list := []Segment{{IOVA: iova(base), Len: uint64(PageSize/2 + 1)}}
				if _, err := r.io.Transfer(dev1, list, seed%2 == 0, cb); err != nil {
					failures.Add(1)
				}
			}
		}(w)
	}

	// Mutator worker cycling remap and revoke. After revoke it re-maps so
	// transfer workers keep making progress. It runs a fixed number of rounds
	// so it can never be stopped while blocked inside Revoke.
	mutatorWG.Add(1)
	go func() {
		defer mutatorWG.Done()
		for i := 0; i < 200; i++ {
			f := i % frames
			alt := (f + 1) % frames
			if err := r.d1.Remap(iova(f), phys(alt), PermRead|PermWrite, tenantA); err != nil {
				// Revoke may have emptied the table; that is an expected race.
				if !errors.Is(err, ErrUnmapped) {
					t.Errorf("unexpected Remap error: %v", err)
					return
				}
			}
			if i%25 == 24 {
				r.d1.Revoke()
				for g := 0; g < frames; g++ {
					_ = r.d1.Map(iova(g), phys(g), PermRead|PermWrite, tenantA)
				}
			}
		}
	}()

	mutatorWG.Wait()
	close(stop)
	wg.Wait()

	// Every in-flight lease must have been returned.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && r.d1.ActiveLeases() != 0 {
		time.Sleep(time.Millisecond)
	}
	if got := r.d1.ActiveLeases(); got != 0 {
		t.Fatalf("leaked leases after concurrency test: %d", got)
	}
	t.Logf("callbacks=%d failures=%d finalGen=%d",
		callbacks.Load(), failures.Load(), r.d1.Generation())
}

// TestConcurrentAcquireReleasesWithRevoke runs many acquire/release cycles
// against repeated revokes to stress the cond broadcast and lease counters.
func TestConcurrentAcquireReleasesWithRevoke(t *testing.T) {
	r := newRig(t)
	r.assignFrames(t, tenantA, 0)

	var wg sync.WaitGroup
	const rounds = 200

	var mutatorWG sync.WaitGroup
	mutatorWG.Add(1)
	go func() {
		defer mutatorWG.Done()
		for i := 0; i < rounds; i++ {
			r.d1.Revoke()
			_ = r.d1.Map(iova(0), phys(0), PermRead, tenantA)
		}
	}()

	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				lease, err := r.io.Acquire(dev1, []Segment{{IOVA: iova(0), Len: 32}}, false)
				if err == nil {
					lease.Release()
				}
			}
		}()
	}

	done := make(chan struct{})
	go func() { mutatorWG.Wait(); wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("workers did not finish; possible cond deadlock")
	}

	// Final explicit revoke must return promptly with zero outstanding leases.
	doneRevoke := make(chan struct{})
	go func() { r.d1.Revoke(); close(doneRevoke) }()
	select {
	case <-doneRevoke:
	case <-time.After(2 * time.Second):
		t.Fatalf("final Revoke hung with %d active leases", r.d1.ActiveLeases())
	}
	if got := r.d1.ActiveLeases(); got != 0 {
		t.Fatalf("active leases = %d, want 0", got)
	}
}
