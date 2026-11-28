package compaction

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrentSchedulingFlushes runs background compaction workers while a
// writer keeps publishing L0 files, asserting no lock corruption, no
// overlapping levels and clean shutdown.
func TestConcurrentSchedulingFlushes(t *testing.T) {
	cfg := testConfig()
	cfg.L0Trigger = 2
	cfg.LevelTargetBytes = []int64{0, 200, 200, 200}
	cfg.Stall.L0SoftDelayFiles = 1000 // keep the test writing
	cfg.Stall.L0HardStopFiles = 1000
	e := NewEngine(cfg)

	var done int64
	sched := NewScheduler(e)
	sched.MaxConcurrent = 3
	sched.Interval = time.Millisecond
	sched.Start()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx := context.Background()
		for i := 0; i < 60; i++ {
			if err := e.Put(ctx, UserKey("k"), []byte("v")); err != nil {
				t.Errorf("Put: %v", err)
				return
			}
			key := UserKey("key-" + string(rune('a'+i%26)) + "-" + string(rune('a'+(i/26)%26)))
			e.Flush([]Entry{putEntry(key, e.AllocateSeq(), "v")}, nil)
			time.Sleep(time.Microsecond)
		}
		atomic.StoreInt64(&done, 1)
	}()

	// Let workers drain everything, then validate the final layout.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&done) == 1 && e.ActiveJobCount() == 0 &&
			e.L0FileCount() < cfg.L0Trigger {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	sched.Close()
	wg.Wait()

	// A residual L0 count below the trigger is correct: such files are not
	// eligible until more flushes arrive.
	if e.L0FileCount() >= cfg.L0Trigger {
		t.Fatalf("L0 backlog %d never drained below trigger %d",
			e.L0FileCount(), cfg.L0Trigger)
	}
	if e.ActiveJobCount() != 0 {
		t.Fatalf("%d jobs leaked after scheduler close", e.ActiveJobCount())
	}
	if err := e.CheckInvariants(); err != nil {
		t.Fatalf("final layout violates invariants: %v", err)
	}
	// All locks must have been released.
	if len(e.lockedFiles) != 0 {
		t.Fatalf("file locks leaked: %v", e.lockedFiles)
	}
}

// TestParallelSubcompactionsAreDisjoint stresses a single multi-span
// compaction and confirms output files never overlap.
func TestParallelSubcompactionsAreDisjoint(t *testing.T) {
	cfg := testConfig()
	cfg.NumLevels = 2
	cfg.LevelTargetBytes = []int64{0, 80}
	cfg.MaxSubcompactions = 4
	cfg.SubcompactionBytes = 40
	e := NewEngine(cfg)

	for i := 0; i < 12; i++ {
		e.Ingest(1, []Entry{
			putEntry(UserKey("row-"+string(rune('a'+i))), uint64(i+1), bigVal),
		}, nil)
	}

	sched := NewScheduler(e)
	if _, err := sched.RunUntilStable(); err != nil {
		t.Fatalf("RunUntilStable: %v", err)
	}
	if err := e.CheckInvariants(); err != nil {
		t.Fatalf("invariants: %v", err)
	}
	v := e.CurrentVersion()
	if len(v.Files(1)) < 2 {
		t.Fatalf("expected multiple disjoint output SSTs, got %d", len(v.Files(1)))
	}
}
