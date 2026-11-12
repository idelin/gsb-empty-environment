package netsec

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

var fullSet = []CheckID{"quick", "signature", "heuristic", "sandbox"}

func makeResults(checks []CheckID, verdict Verdict, at time.Time) []CheckResult {
	out := make([]CheckResult, 0, len(checks))
	for _, check := range checks {
		if verdict == VerdictPositive {
			out = append(out, NewPositiveResult(check, []string{string(check) + "-hit"}, at))
		} else {
			out = append(out, NewNegativeResult(check, at))
		}
	}
	return out
}

func TestCacheReuseRequiresFullCoverage(t *testing.T) {
	cache := NewCache()
	key := testKey("v1")
	now := time.Now()

	quickOnly := &Conclusion{
		Key:     key,
		Results: []CheckResult{NewNegativeResult("quick", now)},
		Pending: []CheckID{"signature", "heuristic", "sandbox"},
	}
	if _, err := cache.Put(quickOnly); err != nil {
		t.Fatalf("put quick screen: %v", err)
	}

	entry, missing, err := cache.Get(key, fullSet)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if entry == nil {
		t.Fatal("expected the partial entry to be returned")
	}
	if len(missing) != 3 {
		t.Fatalf("missing = %v, want 3 outstanding checks", missing)
	}
	if entry.Decision(fullSet) != StatusIncomplete {
		t.Fatal("partial cache entry must not be reusable as a full pass")
	}

	// The quick check alone is reusable for a quick-only request.
	_, quickMissing, err := cache.Get(key, []CheckID{"quick"})
	if err != nil {
		t.Fatalf("get quick: %v", err)
	}
	if len(quickMissing) != 0 {
		t.Fatalf("quick request missing = %v, want none", quickMissing)
	}

	// Unknown key reports every requested check missing instead of inventing
	// a pass.
	_, unknownMissing, err := cache.Get(
		Key{ContentDigest: "sha256:other", RulesVersion: "v1"}, fullSet)
	if err != nil {
		t.Fatalf("get unknown: %v", err)
	}
	if len(unknownMissing) != len(fullSet) {
		t.Fatalf("unknown missing = %v, want all checks", unknownMissing)
	}
}

func TestCachePutMergesAndIsIdempotent(t *testing.T) {
	cache := NewCache()
	key := testKey("v1")
	now := time.Now()

	first := &Conclusion{
		Key: key,
		Results: []CheckResult{
			NewPositiveResult("signature", []string{"EICAR"}, now),
		},
		Pending: []CheckID{"sandbox"},
	}
	stored, err := cache.Put(first)
	if err != nil {
		t.Fatalf("first put: %v", err)
	}

	// Mutating the caller's slice must not affect the cache.
	first.Results[0].Evidence[0] = "tampered"
	got, _, _ := cache.Get(key, fullSet)
	if r, _ := findResult(got, "signature"); r.Evidence[0] != "EICAR" {
		t.Fatalf("cache entry was mutated through caller slice: %v", r.Evidence)
	}

	later := &Conclusion{
		Key: key,
		Results: []CheckResult{
			NewNegativeResult("signature", now.Add(time.Minute)),
			NewNegativeResult("sandbox", now.Add(time.Minute)),
		},
	}
	for range 3 {
		stored, err = cache.Put(later)
		if err != nil {
			t.Fatalf("put: %v", err)
		}
	}

	if cache.Len() != 1 {
		t.Fatalf("cache len = %d, want 1", cache.Len())
	}
	if stored.Status() != StatusThreat {
		t.Fatalf("status = %s, want threat after negative re-scan", stored.Status())
	}
	r, _ := findResult(stored, "signature")
	if r.Verdict != VerdictPositive || len(r.Evidence) != 1 || r.Evidence[0] != "EICAR" {
		t.Fatalf("evidence not preserved: %+v", r)
	}
	if len(stored.Pending) != 0 {
		t.Fatalf("pending = %v, want empty", stored.Pending)
	}

	// Returned entries are copies too.
	stored.Results[0].Evidence = append(stored.Results[0].Evidence, "injected")
	again, _, _ := cache.Get(key, fullSet)
	if r, _ := findResult(again, "signature"); len(r.Evidence) != 1 {
		t.Fatal("cache entry was mutated through returned slice")
	}
}

func TestRuleVersionUpgradeIsolatesEntries(t *testing.T) {
	cache := NewCache()
	now := time.Now()
	v1 := testKey("v1")
	v2 := testKey("v2")

	fullNegative := &Conclusion{
		Key:     v1,
		Results: makeResults(fullSet, VerdictNegative, now),
	}
	if _, err := cache.Put(fullNegative); err != nil {
		t.Fatalf("put v1: %v", err)
	}

	// v1 fully covers the request under v1 ...
	if entry, missing, _ := cache.Get(v1, fullSet); len(missing) != 0 ||
		entry.Decision(fullSet) != StatusClean {
		t.Fatalf("v1 full hit broken: missing=%v", missing)
	}

	// ... but a rule upgrade must miss every check and never reuse v1 data.
	entry, missing, err := cache.Get(v2, fullSet)
	if err != nil {
		t.Fatalf("get v2: %v", err)
	}
	if entry != nil {
		t.Fatal("upgraded rule version must not return an old-version entry")
	}
	if len(missing) != len(fullSet) {
		t.Fatalf("v2 missing = %v, want all checks re-run", missing)
	}

	// v2 finds a threat; the old v1 clean entry stays untouched and separate.
	v2Threat := &Conclusion{
		Key:     v2,
		Results: makeResults(fullSet, VerdictPositive, now),
	}
	if _, err := cache.Put(v2Threat); err != nil {
		t.Fatalf("put v2: %v", err)
	}
	if cache.Len() != 2 {
		t.Fatalf("cache len = %d, want 2 versioned entries", cache.Len())
	}
	v1Entry, v1Missing, _ := cache.Get(v1, fullSet)
	if len(v1Missing) != 0 || v1Entry.Decision(fullSet) != StatusClean {
		t.Fatal("v1 entry was contaminated by the v2 upgrade")
	}
	v2Entry, v2Missing, _ := cache.Get(v2, fullSet)
	if len(v2Missing) != 0 || v2Entry.Decision(fullSet) != StatusThreat {
		t.Fatal("v2 threat conclusion not stored correctly")
	}
}

func TestAnalyzeConcurrentCompletionAndCancellation(t *testing.T) {
	key := testKey("v1")

	t.Run("all complete", func(t *testing.T) {
		var mu sync.Mutex
		running := map[CheckID]bool{}
		run := func(_ context.Context, check CheckID) (CheckResult, error) {
			mu.Lock()
			running[check] = true
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			return NewNegativeResult(check, time.Now()), nil
		}

		conclusion, err := Analyze(context.Background(), key, fullSet, run)
		if err != nil {
			t.Fatalf("analyze: %v", err)
		}
		if len(conclusion.Results) != len(fullSet) || len(conclusion.Pending) != 0 {
			t.Fatalf("conclusion = %+v", conclusion)
		}
		if conclusion.Decision(fullSet) != StatusClean {
			t.Fatal("fully completed negative run must be clean")
		}
		if len(running) != len(fullSet) {
			t.Fatalf("ran %d checks, want %d", len(running), len(fullSet))
		}
	})

	t.Run("canceled mid-flight", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		release := make(chan struct{})
		var started sync.WaitGroup
		started.Add(2)

		run := func(runCtx context.Context, check CheckID) (CheckResult, error) {
			if check == "quick" || check == "signature" {
				started.Done()
				<-release // block until the test cancels
			}
			<-runCtx.Done()
			return CheckResult{}, runCtx.Err()
		}

		done := make(chan struct{})
		var conclusion *Conclusion
		var analyzeErr error
		go func() {
			conclusion, analyzeErr = Analyze(ctx, key, fullSet, run)
			close(done)
		}()

		started.Wait()
		cancel()
		close(release)
		<-done

		if !errors.Is(analyzeErr, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", analyzeErr)
		}
		if len(conclusion.Results) != 0 {
			t.Fatalf("no check should complete, got %+v", conclusion.Results)
		}
		if len(conclusion.Pending) != len(fullSet) {
			t.Fatalf("pending = %v, want all checks", conclusion.Pending)
		}
		if conclusion.Status() != StatusIncomplete {
			t.Fatalf("status = %s, want incomplete after cancel", conclusion.Status())
		}
		if conclusion.Decision(fullSet) != StatusIncomplete {
			t.Fatal("canceled run must never be reported as passing")
		}
	})

	t.Run("already canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		conclusion, err := Analyze(ctx, key, fullSet,
			func(context.Context, CheckID) (CheckResult, error) {
				t.Fatal("check must not run with an already-canceled context")
				return CheckResult{}, nil
			})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if len(conclusion.Pending) != len(fullSet) || len(conclusion.Results) != 0 {
			t.Fatalf("conclusion = %+v", conclusion)
		}
	})
}

func TestEnsureRetriesOnlyMissingAfterCancel(t *testing.T) {
	cache := NewCache()
	key := testKey("v1")
	now := time.Now()

	// A canceled first attempt leaves its work cached as pending.
	ctx, cancel := context.WithCancel(context.Background())
	run := func(runCtx context.Context, check CheckID) (CheckResult, error) {
		if check == "sandbox" {
			cancel()
		}
		<-runCtx.Done()
		return CheckResult{}, runCtx.Err()
	}

	entry, missing, err := cache.Ensure(ctx, key, fullSet, run)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ensure err = %v, want canceled", err)
	}
	if entry == nil || len(missing) != len(fullSet) {
		t.Fatalf("after cancel: entry=%v missing=%v", entry, missing)
	}

	// Retry: a successful run produces a complete conclusion.
	var mu sync.Mutex
	seen := map[CheckID]int{}
	retryRun := func(_ context.Context, check CheckID) (CheckResult, error) {
		mu.Lock()
		seen[check]++
		mu.Unlock()
		if check == "signature" {
			return NewPositiveResult(check, []string{"EICAR"}, now), nil
		}
		return NewNegativeResult(check, now), nil
	}

	entry, missing, err = cache.Ensure(context.Background(), key, fullSet, retryRun)
	if err != nil {
		t.Fatalf("retry ensure: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("retry missing = %v, want none", missing)
	}
	if entry.Decision(fullSet) != StatusThreat {
		t.Fatalf("retry decision = %s, want threat", entry.Decision(fullSet))
	}

	// A subsequent full hit executes no checks at all.
	hit, hitMissing, err := cache.Ensure(context.Background(), key, fullSet,
		func(context.Context, CheckID) (CheckResult, error) {
			t.Fatal("cache hit must not re-execute checks")
			return CheckResult{}, nil
		})
	if err != nil || len(hitMissing) != 0 {
		t.Fatalf("full hit: missing=%v err=%v", hitMissing, err)
	}
	if hit.Decision(fullSet) != StatusThreat {
		t.Fatal("full hit lost the positive finding")
	}
	for check, count := range seen {
		if count != 1 {
			t.Fatalf("check %s ran %d times on retry, want once", check, count)
		}
	}
}

func TestConcurrentPutMergesAreRaceFree(t *testing.T) {
	cache := NewCache()
	key := testKey("v1")
	now := time.Now()

	var wg sync.WaitGroup
	for i := range 64 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			check := fullSet[i%len(fullSet)]
			var result CheckResult
			if i%3 == 0 {
				result = NewPositiveResult(
					check, []string{fmt.Sprintf("evidence-%d", i)}, now)
			} else {
				result = NewNegativeResult(check, now)
			}
			if _, err := cache.Put(
				&Conclusion{Key: key, Results: []CheckResult{result}}); err != nil {
				t.Errorf("put %d: %v", i, err)
			}
		}(i)
	}

	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = cache.Get(key, fullSet)
		}()
	}
	wg.Wait()

	entry, missing, _ := cache.Get(key, fullSet)
	if len(missing) != 0 {
		t.Fatalf("missing after concurrent puts: %v", missing)
	}
	if entry.Status() != StatusThreat {
		t.Fatalf("status = %s, want threat (some writers were positive)",
			entry.Status())
	}
}
