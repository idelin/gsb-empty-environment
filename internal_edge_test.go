package radixkv

import (
	"errors"
	"testing"
)

func TestSplitInsidePhysicalBlockKeepsUniqueBlockConservation(t *testing.T) {
	cache := NewRadixCache(4, 4)
	first, err := cache.Insert([]int{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Blocks) != 1 {
		t.Fatalf("initial blocks=%v, want one", first.Blocks)
	}

	second, err := cache.Insert([]int{1, 9, 9, 9, 9})
	if err != nil {
		t.Fatal(err)
	}
	if second == nil {
		t.Fatal("missing divergent node")
	}

	parent := cache.Root().Children[1]
	if parent == nil {
		t.Fatal("missing split parent")
	}
	if got := parent.Tokens; len(got) != 1 || got[0] != 1 {
		t.Fatalf("split parent tokens=%v, want [1]", got)
	}
	if len(parent.Slices) != 1 || parent.Slices[0].BlockID != first.Blocks[0] {
		t.Fatalf("parent slice=%+v should retain prefix part of original block", parent.Slices)
	}

	oldSuffix := parent.Children[2]
	if oldSuffix == nil {
		t.Fatalf("old suffix missing: %+v", parent.Children)
	}
	if len(oldSuffix.Slices) != 1 || oldSuffix.Slices[0].BlockID != first.Blocks[0] {
		t.Fatalf("old suffix should share boundary block slice: %+v", oldSuffix.Slices)
	}

	report := cache.Conservation()
	if !report.Conserved || report.TreeBlocks != 2 || report.FreeBlocks != 2 {
		t.Fatalf("unaligned split conservation failure: %+v", report)
	}

	// Evicting the old suffix drops one slice reference but must not free the
	// boundary block because the prefix slice still references it.
	evicted := cache.EvictLRU()
	if evicted == nil || evicted.ID != oldSuffix.ID {
		t.Fatalf("expected old suffix eviction, got %+v", evicted)
	}
	report = cache.Conservation()
	if !report.Conserved || report.TreeBlocks != 2 {
		t.Fatalf("boundary block disappeared while prefix still uses it: %+v", report)
	}
}

func TestSelectVictimPriorityThenNewest(t *testing.T) {
	scheduler := NewScheduler(8, 1, 4)
	oldest := scheduler.AddRequest("oldest", []int{1}, 4)
	lowOld := scheduler.AddRequest("low-old", []int{2}, 4)
	lowNew := scheduler.AddRequest("low-new", []int{3}, 4)
	highNew := scheduler.AddRequest("high-new", []int{4}, 4)

	for _, req := range []*Request{oldest, lowOld, lowNew, highNew} {
		req.setState(StateDecoding)
		scheduler.running[req.ID] = req
		req.Priority = 10
	}
	lowOld.Priority = 0
	lowNew.Priority = 0
	highNew.Priority = 5

	victim := scheduler.selectVictimLocked(highNew.ID)
	if victim == nil || victim.ID != "low-new" {
		t.Fatalf("victim=%v, want low-new", victim)
	}

	lowOld.ArrivalSeq = 99
	victim = scheduler.selectVictimLocked(highNew.ID)
	if victim == nil || victim.ID != "low-old" {
		t.Fatalf("victim=%v, want low-old when it is newest among low priority", victim)
	}

	// The globally oldest decoder is protected even if it has low priority.
	oldest.Priority = -100
	victim = scheduler.selectVictimLocked(highNew.ID)
	if victim == nil || victim.ID != "low-old" {
		t.Fatalf("oldest decoder must be protected, victim=%v", victim)
	}
}

func TestSelfPreemptionReturnsSentinel(t *testing.T) {
	scheduler := NewScheduler(1, 1, 1)
	older := scheduler.AddRequest("older-decoder", []int{0}, 8)
	older.setState(StateDecoding)
	scheduler.running[older.ID] = older

	req := scheduler.AddRequest("only-newest", []int{1}, 8)
	req.setState(StateDecoding)
	scheduler.running[req.ID] = req
	id, err := scheduler.cache.AllocateRunningBlock(req.ID)
	if err != nil {
		t.Fatal(err)
	}
	req.BlockIDs = append(req.BlockIDs, id)

	result := newStepResult(1, 1)
	err = scheduler.acquireBlocksWithPreemptionLocked(req, 1, result)
	if !errors.Is(err, ErrRequestPreempted) {
		t.Fatalf("err=%v, want ErrRequestPreempted", err)
	}
	if req.State != StateWaiting || len(req.BlockIDs) != 0 {
		t.Fatalf("self victim should be waiting with no blocks: state=%s blocks=%v", req.State, req.BlockIDs)
	}
	if len(result.PreemptedRequestIDs) != 1 || result.PreemptedRequestIDs[0] != req.ID {
		t.Fatalf("preempted=%v", result.PreemptedRequestIDs)
	}
}

func TestInsertEvictsUnrelatedLeafButProtectsMatchPath(t *testing.T) {
	cache := NewRadixCache(4, 1)
	matched, err := cache.Insert([]int{10, 20})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Insert([]int{30}); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Insert([]int{40}); err != nil {
		t.Fatal(err)
	}

	extended, err := cache.Insert([]int{10, 20, 50})
	if err != nil {
		t.Fatalf("insert extension should evict unrelated LRU leaf: %v", err)
	}
	if extended.Parent == nil || extended.Parent.ID != matched.ID {
		t.Fatalf("new node should remain attached to protected match path")
	}
	report := cache.Conservation()
	if !report.Conserved {
		t.Fatalf("conservation failure: %+v", report)
	}
}
