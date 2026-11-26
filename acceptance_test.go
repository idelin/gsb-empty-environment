package radixkv_test

import (
	"fmt"
	"testing"

	radixkv "prescreen.local/ps-fd9b22029b"
)

func assertConserved(t *testing.T, label string, report radixkv.ConservationReport) {
	t.Helper()
	if !report.Conserved || report.Sum != report.TotalBlocks {
		t.Fatalf("%s: conservation failure: %+v problems=%v", label, report, report.Problems)
	}
}

func assertIntSlice(t *testing.T, label string, got, want []int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: len=%d want %d (%v vs %v)", label, len(got), len(want), got, want)
	}
	for i := range want {
		if int(got[i]) != want[i] {
			t.Fatalf("%s: at %d got %d want %d (full got=%v want=%v)", label, i, got[i], want[i], got, want)
		}
	}
}

// 1) Splitting the middle of an edge preserves inherited refs and supports two
// requests decoding concurrently on the shared prefix.
func TestAcceptanceSplitSharedPrefixDualDecode(t *testing.T) {
	const blockSize = 2
	cache := radixkv.NewRadixCache(16, blockSize)
	sharedLong := []int{1, 2, 3, 4, 5, 6, 7, 8}
	if _, err := cache.Insert(sharedLong); err != nil {
		t.Fatal(err)
	}

	scheduler := radixkv.NewSchedulerWithCache(cache, 8)
	reqA := scheduler.AddRequest("a", sharedLong, 8)

	step1, err := scheduler.Step()
	if err != nil {
		t.Fatal(err)
	}
	assertConserved(t, "step1", step1.Conservation)
	if step1.SkippedCachedTokens["a"] != len(sharedLong) {
		t.Fatalf("expected full prefix skip, got %+v", step1.SkippedCachedTokens)
	}
	if reqA.State != radixkv.StateDecoding {
		t.Fatalf("request a state=%s, want decoding", reqA.State)
	}

	reqB := scheduler.AddRequest("b", []int{1, 2, 3, 4, 20, 21, 22, 23}, 8)
	step2, err := scheduler.Step()
	if err != nil {
		t.Fatal(err)
	}
	assertConserved(t, "step2", step2.Conservation)

	prefix := cache.Root().Children[1]
	if prefix == nil {
		t.Fatal("missing shared prefix node after split")
	}
	assertIntSlice(t, "split prefix tokens", prefix.Tokens, []int{1, 2, 3, 4})
	assertIntSlice(t, "split prefix blocks", intSliceToInt(prefix.Blocks), []int{0, 1})

	suffix := prefix.Children[5]
	if suffix == nil {
		t.Fatalf("missing original suffix child: %+v", prefix.Children)
	}
	assertIntSlice(t, "split suffix tokens", suffix.Tokens, []int{5, 6, 7, 8})
	assertIntSlice(t, "split suffix blocks", intSliceToInt(suffix.Blocks), []int{2, 3})

	// A locked the original edge before the split. The new prefix inherits that
	// ref, then B locks the prefix, while A still owns the suffix path.
	if prefix.RefCount != 2 {
		t.Fatalf("prefix refCount=%d, want 2", prefix.RefCount)
	}
	if suffix.RefCount != 1 {
		t.Fatalf("suffix refCount=%d, want 1", suffix.RefCount)
	}
	if reqA.State != radixkv.StateDecoding || reqB.State != radixkv.StateDecoding {
		t.Fatalf("states after prefill: a=%s b=%s", reqA.State, reqB.State)
	}

	step3, err := scheduler.Step()
	if err != nil {
		t.Fatal(err)
	}
	assertConserved(t, "step3", step3.Conservation)
	if step3.DecodeTokens["a"] != 1 || step3.DecodeTokens["b"] != 1 {
		t.Fatalf("expected both decoders to run, got %+v", step3.DecodeTokens)
	}
	if prefix.RefCount != 2 || suffix.RefCount != 1 {
		t.Fatalf("refs during dual decode: prefix=%d suffix=%d", prefix.RefCount, suffix.RefCount)
	}

	report := scheduler.Conservation()
	assertConserved(t, "final", report)
	if report.TreeBlocks != 4 || report.RunningBlocks != 4 || report.FreeBlocks != 8 {
		t.Fatalf("unexpected block distribution: %+v", report)
	}
}

// 2) A parent enters the evictable leaf heap only after every child is gone and
// its own refCount is zero; unlocking such a parent enqueues it immediately.
func TestAcceptanceParentReclaimOnlyAfterAllChildren(t *testing.T) {
	cache := radixkv.NewRadixCache(10, 1)
	if _, err := cache.Insert([]int{1, 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Insert([]int{1, 3}); err != nil {
		t.Fatal(err)
	}

	parent := cache.Root().Children[1]
	if parent == nil || len(parent.Children) != 2 {
		t.Fatalf("expected parent with two children, got %+v", parent)
	}
	if err := cache.LockNode("holder", parent); err != nil {
		t.Fatal(err)
	}

	first := cache.EvictLRU()
	if first == nil {
		t.Fatal("expected first LRU child eviction")
	}
	assertIntSlice(t, "first evicted tokens", first.Tokens, []int{2})
	if ids := cache.LRUNodeIDs(); containsInt(ids, parent.ID) {
		t.Fatalf("parent became evictable while another child remains: heap ids=%v", ids)
	}

	second := cache.EvictLRU()
	if second == nil {
		t.Fatal("expected second LRU child eviction")
	}
	assertIntSlice(t, "second evicted tokens", second.Tokens, []int{3})
	if !parent.IsLeaf() {
		t.Fatal("parent should be a leaf after both children evicted")
	}

	// It is a leaf now, but the request lock still protects it.
	if node := cache.EvictLRU(); node != nil {
		t.Fatalf("locked parent was evicted: %+v", node)
	}
	if parent.RefCount != 1 {
		t.Fatalf("parent refCount=%d, want 1", parent.RefCount)
	}

	cache.UnlockRequest("holder")
	evictedParent := cache.EvictLRU()
	if evictedParent == nil || evictedParent.ID != parent.ID {
		t.Fatalf("expected parent %d after unlock, got %+v", parent.ID, evictedParent)
	}

	report := cache.Conservation()
	assertConserved(t, "eviction audit", report)
	if report.TreeBlocks != 0 || report.FreeBlocks != report.TotalBlocks {
		t.Fatalf("expected all blocks free, got %+v", report)
	}
}

// 3) A long prefill advances in chunks while cached prefix blocks are skipped.
func TestAcceptanceChunkedPrefillSkipsCachedPrefix(t *testing.T) {
	cache := radixkv.NewRadixCache(12, 2)
	if _, err := cache.Insert([]int{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	scheduler := radixkv.NewSchedulerWithCache(cache, 2)
	req := scheduler.AddRequest("long", []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 4)

	for step := 1; step <= 3; step++ {
		result, err := scheduler.Step()
		if err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		assertConserved(t, fmt.Sprintf("prefill step %d", step), result.Conservation)
		if result.PrefillTokens["long"] != 2 {
			t.Fatalf("step %d prefill tokens=%d, want 2: %+v", step, result.PrefillTokens["long"], result)
		}
		if step == 1 && result.SkippedCachedTokens["long"] != 4 {
			t.Fatalf("cached prefix skip=%d, want 4", result.SkippedCachedTokens["long"])
		}
		if req.PrefillCursor != 4+2*step {
			t.Fatalf("step %d cursor=%d, want %d", step, req.PrefillCursor, 4+2*step)
		}
	}

	if req.ComputedPromptTokens != 6 {
		t.Fatalf("computed prompt tokens=%d, want only six non-cached tokens", req.ComputedPromptTokens)
	}
	if req.State != radixkv.StateDecoding {
		t.Fatalf("state=%s, want decoding after chunked prefill", req.State)
	}
	report := scheduler.Conservation()
	if report.TreeBlocks != 2 || report.RunningBlocks != 3 || report.FreeBlocks != 7 {
		t.Fatalf("unexpected chunked-prefill distribution: %+v", report)
	}
}

// 4) When no LRU leaf can be evicted, the newest running request is preempted so
// the oldest decoder can make progress without deadlock.
func TestAcceptanceTightBudgetPreemptionNoDeadlock(t *testing.T) {
	scheduler := radixkv.NewScheduler(4, 4, 12)
	oldest := scheduler.AddRequest("oldest", []int{1, 2, 3, 4}, 32)

	setup, err := scheduler.Step()
	if err != nil {
		t.Fatal(err)
	}
	assertConserved(t, "setup", setup.Conservation)

	newest := scheduler.AddRequest(
		"newest",
		[]int{10, 11, 12, 13, 20, 21, 22, 23, 30, 31, 32, 33},
		32,
	)

	var preemptStep *radixkv.StepResult
	for i := 0; i < 12; i++ {
		result, stepErr := scheduler.Step()
		if stepErr != nil {
			t.Fatalf("step %d failed before/after preemption: %v", i+2, stepErr)
		}
		assertConserved(t, fmt.Sprintf("tight step %d", i+2), result.Conservation)
		if len(result.PreemptedRequestIDs) > 0 {
			preemptStep = result
			break
		}
	}

	if preemptStep == nil {
		t.Fatal("expected KV pressure to trigger preemption")
	}
	if len(preemptStep.PreemptedRequestIDs) != 1 || preemptStep.PreemptedRequestIDs[0] != "newest" {
		t.Fatalf("preempted=%v, want only newest", preemptStep.PreemptedRequestIDs)
	}
	if preemptStep.DecodeTokens["oldest"] != 1 {
		t.Fatalf("oldest decoder did not advance in preemption step: %+v", preemptStep.DecodeTokens)
	}
	if newest.State != radixkv.StateWaiting {
		t.Fatalf("preempted newest state=%s, want waiting", newest.State)
	}
	if newest.PreemptionCount != 1 || len(newest.BlockIDs) != 0 {
		t.Fatalf("newest preemption=%d blocks=%v, want one preemption and no blocks",
			newest.PreemptionCount, newest.BlockIDs)
	}
	if oldest.State != radixkv.StateDecoding || len(oldest.Generated) == 0 {
		t.Fatalf("oldest did not remain decoding/progress: state=%s generated=%d",
			oldest.State, len(oldest.Generated))
	}

	// A following step must continue to obey the invariant and admit no deadlock.
	after, err := scheduler.Step()
	if err != nil {
		t.Fatal(err)
	}
	assertConserved(t, "after preemption", after.Conservation)
	if after.DecodeTokens["oldest"] != 1 {
		t.Fatalf("oldest did not advance after preemption: %+v", after.DecodeTokens)
	}
}

// 5) A mixed deterministic workload checks strict conservation on every step.
func TestAcceptanceMixedWorkloadConservationEveryStep(t *testing.T) {
	scheduler := radixkv.NewScheduler(14, 2, 5)
	cache := scheduler.Cache()
	if _, err := cache.Insert([]int{1, 2, 3, 4, 5, 6}); err != nil {
		t.Fatal(err)
	}

	prompts := [][]int{
		{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
		{1, 2, 3, 4, 20, 21, 22, 23, 24},
		{100, 101, 102, 103, 104, 105, 106, 107},
		{1, 2, 3, 4, 5, 6, 30, 31, 32, 33},
	}
	for i, prompt := range prompts {
		scheduler.AddRequest(fmt.Sprintf("r%d", i), prompt, 5+i)
	}

	for step := 1; step <= 80; step++ {
		result, err := scheduler.Step()
		if err != nil {
			t.Fatalf("step %d: %v\nreport=%+v", step, err, result.Conservation)
		}
		label := fmt.Sprintf("mixed step %d", step)
		assertConserved(t, label, result.Conservation)
		if result.UsedTokens > scheduler.MaxTokensPerStep() {
			t.Fatalf("step %d used %d > budget %d", step, result.UsedTokens, scheduler.MaxTokensPerStep())
		}
		if scheduler.AllDone() {
			return
		}
	}
	t.Fatal("mixed workload did not finish within 80 steps")
}

func intSliceToInt(ids []radixkv.BlockID) []int {
	out := make([]int, len(ids))
	for i, id := range ids {
		out[i] = int(id)
	}
	return out
}

func containsInt(ids []int, target int) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}
