package netsec

import (
	"bytes"
	"testing"
	"time"
)

var testRegion = Region{Start: 0x1000, End: 0x1010}

// TestBasicMigration patches a page while two threads sit on migratable
// boundaries and a third one runs outside the region.
func TestBasicMigration(t *testing.T) {
	host := NewSimHost(testRegion)
	host.Fill(testPlan().OldBytes)

	first := host.Spawn(1, 0x1000, 0)
	second := host.Spawn(2, 0x1008, 0)
	outside := host.Spawn(3, 0x9000, 0)

	opts := Options{
		MaxAttempts: 2,
		FreezeWait:  50 * time.Millisecond,
		RetryDelay:  time.Millisecond,
		Sleep:       func(time.Duration) {},
		Now:         time.Now,
	}

	result, err := ApplyPatch(host, testPlan(), opts)
	if err != nil {
		t.Fatalf("ApplyPatch failed: %v", err)
	}
	if result.Attempts != 1 || result.MigratedThreads != 2 || result.OutsideThreads != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}

	page, err := host.ReadMemory(testRegion.Start, 16)
	if err != nil {
		t.Fatalf("ReadMemory: %v", err)
	}
	if !bytes.Equal(page, testPlan().NewBytes) {
		t.Fatalf("page not rewritten: % x", page)
	}
	if first.PC() != 0x1000 || second.PC() != 0x1008 || outside.PC() != 0x9000 {
		t.Fatalf("unexpected PCs: first=%#x second=%#x outside=%#x",
			first.PC(), second.PC(), outside.PC())
	}
	if first.IsFrozen() || second.IsFrozen() || outside.IsFrozen() {
		t.Fatal("threads were not resumed after the patch")
	}
	if host.ResumeRequests != 3 {
		t.Fatalf("expected one resume per thread, got %d", host.ResumeRequests)
	}
}

func testPlan() Plan {
	oldBytes := bytes.Repeat([]byte{0xAA}, 16)
	newBytes := bytes.Repeat([]byte{0xBB}, 16)
	return Plan{
		Region:     testRegion,
		OldBytes:   oldBytes,
		NewBytes:   newBytes,
		OldToNewPC: map[Addr]Addr{0x1000: 0x1000, 0x1008: 0x1008},
	}
}

// TestLateFreeze freezes one thread immediately and another one only after a
// scheduler delay. The coordinator must keep polling inside FreezeWait and
// only classify or resume after every thread has actually frozen.
func TestLateFreeze(t *testing.T) {
	host := NewSimHost(testRegion)
	host.Fill(testPlan().OldBytes)

	prompt := host.Spawn(1, 0x1000, 0)
	late := host.Spawn(2, 0x2000, 5*time.Millisecond)

	observed := make(chan struct{})
	opts := Options{
		MaxAttempts: 4,
		FreezeWait:  250 * time.Millisecond,
		RetryDelay:  time.Millisecond,
		Sleep:       time.Sleep,
		Now:         time.Now,
		OnRound: func(attempt int, threads []frozenThread) {
			if attempt != 1 || len(threads) != 2 {
				return
			}
			for _, thread := range threads {
				if thread.class == classExited {
					t.Errorf("no thread should be exited in this scenario")
				}
			}
			if !prompt.IsFrozen() || !late.IsFrozen() {
				t.Errorf("barrier opened before every thread froze: prompt=%v late=%v",
					prompt.IsFrozen(), late.IsFrozen())
			}
			close(observed)
		},
	}

	result, err := ApplyPatch(host, testPlan(), opts)
	if err != nil {
		t.Fatalf("ApplyPatch failed: %v", err)
	}
	select {
	case <-observed:
	default:
		t.Fatal("freeze barrier hook never ran")
	}
	if result.Attempts != 1 {
		t.Fatalf("expected single attempt, got %d", result.Attempts)
	}
	if result.FrozenThreads != 2 || result.MigratedThreads != 1 || result.OutsideThreads != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}

	page, err := host.ReadMemory(testRegion.Start, 16)
	if err != nil {
		t.Fatalf("ReadMemory: %v", err)
	}
	if !bytes.Equal(page, testPlan().NewBytes) {
		t.Fatalf("page not rewritten: % x", page)
	}

	if host.FlushCount() != 1 {
		t.Fatalf("expected exactly one cache flush, got %d", host.FlushCount())
	}

	host.Idle(nil)
	if prompt.IsFrozen() || late.IsFrozen() {
		t.Fatal("threads were not resumed together after the patch")
	}
	if prompt.PC() != 0x1000 || late.PC() != 0x2000 {
		t.Fatalf("unexpected PCs after resume: prompt=%#x late=%#x", prompt.PC(), late.PC())
	}
}

// TestReusedThreadID exits a thread while frozen, hands its ID to a new
// generation that pauses at a non-boundary address, and then lets the new
// occupant leave the region. The stale generation reference must never be
// redirected; the coordinator drives the transition through deterministic
// round hooks instead of wall-clock scheduling.
func TestReusedThreadID(t *testing.T) {
	host := NewSimHost(testRegion)
	host.Fill(testPlan().OldBytes)

	victim := host.Spawn(7, 0x1000, 0)
	other := host.Spawn(8, 0x3000, 0)

	victimRef := ThreadRef{ID: 7, Gen: victim.gen}
	reusedExited := false
	reusedLeftRegion := false

	opts := Options{
		MaxAttempts: 4,
		FreezeWait:  50 * time.Millisecond,
		RetryDelay:  time.Millisecond,
		Sleep:       func(time.Duration) {},
		Now:         time.Now,
		OnRound: func(attempt int, threads []frozenThread) {
			switch attempt {
			case 1:
				// Both original generations are frozen at the barrier.
				if len(threads) != 2 {
					t.Fatalf("round 1: expected 2 frozen threads, got %d", len(threads))
				}
				for _, thread := range threads {
					if thread.class == classExited {
						t.Fatalf("round 1: nothing has exited yet")
					}
				}
				// The old generation exits while frozen; its ID is reused
				// by a thread paused at a non-boundary instruction.
				victim.Exit()
				host.Spawn(7, 0x1004, 0)
				reusedExited = true
			case 2:
				// The stale generation has dropped out of the live set;
				// the reused generation blocks because 0x1004 is not a
				// declared boundary.
				foundReused := false
				for _, thread := range threads {
					if thread.ref == victimRef {
						t.Fatalf("stale generation must not appear in later rounds")
					}
					if thread.ref == (ThreadRef{ID: 7, Gen: 2}) {
						if thread.class != classUnmappable {
							t.Fatalf("reused generation must be unmappable, got %d", thread.class)
						}
						foundReused = true
					}
				}
				if !foundReused {
					t.Fatal("round 2: reused generation missing")
				}
				// Simulate the reused thread naturally leaving the region
				// before the next freeze round. It remains frozen for the
				// rest of this round; resumeAll releases the barrier first.
				if reused := host.Thread(7); reused != nil && reused.PC() == 0x1004 {
					reused.mu.Lock()
					reused.pc = 0x4000
					reused.mu.Unlock()
					reusedLeftRegion = true
				}
			case 3:
				// The reused thread is now outside the region; nothing
				// inside the patched sequence is unmappable.
				for _, thread := range threads {
					if thread.class == classUnmappable {
						t.Fatalf("round 3 must not be blocked, got %+v", thread)
					}
				}
			}
		},
	}

	result, err := ApplyPatch(host, testPlan(), opts)
	if err != nil {
		t.Fatalf("ApplyPatch failed: %v", err)
	}
	if !reusedExited || !reusedLeftRegion {
		t.Fatalf("scenario did not run: exited=%v left=%v", reusedExited, reusedLeftRegion)
	}
	if result.Attempts != 3 {
		t.Fatalf("expected success on attempt 3, got %d", result.Attempts)
	}

	reused := host.Thread(7)
	if reused == nil || reused.gen != 2 {
		t.Fatalf("ID 7 must belong to the second generation, got %+v", reused)
	}
	if reused.PC() != 0x4000 {
		t.Fatalf("reused thread was redirected despite being outside: %#x", reused.PC())
	}
	if victim.PC() != 0x1000 {
		t.Fatalf("stale generation reference must stay untouched, got PC %#x", victim.PC())
	}
	if other.PC() != 0x3000 {
		t.Fatalf("outside-region thread must stay untouched, got PC %#x", other.PC())
	}
	if result.MigratedThreads != 0 || result.OutsideThreads != 2 || result.ExitedThreads != 0 {
		t.Fatalf("unexpected final-round counts: %+v", result)
	}

	page, readErr := host.ReadMemory(testRegion.Start, 16)
	if readErr != nil {
		t.Fatalf("ReadMemory: %v", readErr)
	}
	if !bytes.Equal(page, testPlan().NewBytes) {
		t.Fatalf("page not rewritten: % x", page)
	}
}

// TestFlushFailure forces FlushICache to fail. The coordinator must roll the
// page back to the old bytes, restore any redirected PCs, resume every
// thread and surface the flush error.
func TestFlushFailure(t *testing.T) {
	host := NewSimHost(testRegion)
	host.Fill(testPlan().OldBytes)
	host.SetFlushFailure(true)

	mapped := host.Spawn(1, 0x1008, 0)
	outside := host.Spawn(2, 0x5000, 0)

	opts := Options{
		MaxAttempts: 2,
		FreezeWait:  100 * time.Millisecond,
		RetryDelay:  time.Millisecond,
		Sleep:       time.Sleep,
		Now:         time.Now,
	}

	result, err := ApplyPatch(host, testPlan(), opts)
	if err != ErrFlushFailed {
		t.Fatalf("expected ErrFlushFailed, got %v", err)
	}
	if result.Attempts != 1 {
		t.Fatalf("flush failure must abort on the first attempt, got %d", result.Attempts)
	}

	page, readErr := host.ReadMemory(testRegion.Start, 16)
	if readErr != nil {
		t.Fatalf("ReadMemory: %v", readErr)
	}
	if !bytes.Equal(page, testPlan().OldBytes) {
		t.Fatalf("rollback did not restore old bytes: % x", page)
	}
	if mapped.PC() != 0x1008 {
		t.Fatalf("rollback did not restore migrated PC: %#x", mapped.PC())
	}
	if outside.PC() != 0x5000 {
		t.Fatalf("outside thread PC changed: %#x", outside.PC())
	}
	if mapped.IsFrozen() || outside.IsFrozen() {
		t.Fatal("threads remained frozen after the failed flush")
	}
	if host.FlushCount() != 2 {
		t.Fatalf("expected failed flush plus rollback flush, got %d", host.FlushCount())
	}
}

// TestUnmappableGivesUp keeps a thread parked at a non-boundary address
// inside the patched region. Every attempt must resume all threads and
// retry until MaxAttempts, then fail without touching the page.
func TestUnmappableGivesUp(t *testing.T) {
	host := NewSimHost(testRegion)
	host.Fill(testPlan().OldBytes)

	stuck := host.Spawn(1, 0x1004, 0)
	other := host.Spawn(2, 0x1000, 0)

	var retrySleeps int
	opts := Options{
		MaxAttempts: 3,
		FreezeWait:  50 * time.Millisecond,
		RetryDelay:  2 * time.Millisecond,
		Sleep: func(d time.Duration) {
			if d == 2*time.Millisecond {
				retrySleeps++
			}
		},
		Now: time.Now,
	}

	result, err := ApplyPatch(host, testPlan(), opts)
	if err != ErrRetriesExhausted {
		t.Fatalf("expected ErrRetriesExhausted, got %v", err)
	}
	if result.Attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", result.Attempts)
	}
	if retrySleeps != 2 {
		t.Fatalf("expected two retry delays, got %d", retrySleeps)
	}

	page, readErr := host.ReadMemory(testRegion.Start, 16)
	if readErr != nil {
		t.Fatalf("ReadMemory: %v", readErr)
	}
	if !bytes.Equal(page, testPlan().OldBytes) {
		t.Fatalf("page must stay untouched on failure: % x", page)
	}
	if host.FlushCount() != 0 {
		t.Fatalf("no cache flush expected when unmappable, got %d", host.FlushCount())
	}
	if stuck.PC() != 0x1004 || other.PC() != 0x1000 {
		t.Fatalf("PCs changed despite failed patch: stuck=%#x other=%#x", stuck.PC(), other.PC())
	}
	if stuck.IsFrozen() || other.IsFrozen() {
		t.Fatal("threads remained frozen after giving up")
	}
}
