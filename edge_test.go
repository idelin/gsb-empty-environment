package netsec

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSnapshotAndInvalidInputs(t *testing.T) {
	cache := NewCache()
	now := time.Now()

	keys := []Key{
		{ContentDigest: "sha256:zzz", RulesVersion: "v2"},
		{ContentDigest: "sha256:aaa", RulesVersion: "v1"},
		{ContentDigest: "sha256:aaa", RulesVersion: "v2"},
	}
	for i, key := range keys {
		if _, err := cache.Put(&Conclusion{
			Key:     key,
			Results: []CheckResult{NewNegativeResult("quick", now.Add(time.Duration(i)))},
		}); err != nil {
			t.Fatalf("put: %v", err)
		}
	}

	snap := cache.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("snapshot len = %d, want 3", len(snap))
	}
	wantOrder := []Key{keys[1], keys[2], keys[0]}
	for i, want := range wantOrder {
		if snap[i].Key != want {
			t.Fatalf("snapshot order[%d] = %+v, want %+v", i, snap[i].Key, want)
		}
	}

	// Snapshot returns independent copies.
	snap[0].Results[0].Verdict = VerdictPositive
	again := cache.Snapshot()
	if again[0].Results[0].Verdict != VerdictNegative {
		t.Fatal("snapshot mutation leaked into the cache")
	}

	if _, err := cache.Put(nil); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("nil put: got %v", err)
	}
	if _, err := cache.Put(&Conclusion{Key: Key{ContentDigest: "x"}}); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("bad put key: got %v", err)
	}
	if _, _, err := cache.Get(Key{ContentDigest: "x"}, fullSet); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("bad get key: got %v", err)
	}
	if _, _, err := cache.Ensure(context.Background(), Key{RulesVersion: "v1"}, fullSet, nil); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("bad ensure key: got %v", err)
	}
}

func TestAnalyzeErrorPaths(t *testing.T) {
	key := testKey("v1")

	t.Run("check failure stays pending", func(t *testing.T) {
		run := func(_ context.Context, check CheckID) (CheckResult, error) {
			if check == "heuristic" {
				return CheckResult{}, errors.New("scanner crashed")
			}
			if check == "sandbox" {
				return CheckResult{Check: "sandbox", Verdict: Verdict(42)}, nil
			}
			return NewNegativeResult(check, time.Now()), nil
		}

		conclusion, err := Analyze(context.Background(), key, fullSet, run)
		if err == nil {
			t.Fatal("expected an error from failed checks")
		}
		if len(conclusion.Results) != 2 {
			t.Fatalf("results = %+v, want the two successful checks", conclusion.Results)
		}
		if len(conclusion.Pending) != 2 {
			t.Fatalf("pending = %v, want failed checks recorded as pending", conclusion.Pending)
		}
		if conclusion.Status() != StatusIncomplete {
			t.Fatal("failures must not be wrapped into a pass")
		}

		// The partial conclusion is still cacheable.
		cache := NewCache()
		if _, err := cache.Put(conclusion); err != nil {
			t.Fatalf("cache partial conclusion: %v", err)
		}
	})

	t.Run("invalid arguments", func(t *testing.T) {
		if _, err := Analyze(context.Background(), Key{ContentDigest: "x"}, fullSet, nil); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("bad key: got %v", err)
		}
		if _, err := Analyze(context.Background(), key, []CheckID{""}, nil); !errors.Is(err, ErrInvalidCheck) {
			t.Fatalf("empty check: got %v", err)
		}
		empty, err := Analyze(context.Background(), key, nil, nil)
		if err != nil || len(empty.Results) != 0 || len(empty.Pending) != 0 {
			t.Fatalf("empty check set: conclusion=%+v err=%v", empty, err)
		}
	})

	t.Run("deduplicates requested checks", func(t *testing.T) {
		var calls int
		run := func(_ context.Context, check CheckID) (CheckResult, error) {
			calls++
			return NewNegativeResult(check, time.Now()), nil
		}
		conclusion, err := Analyze(context.Background(), key,
			[]CheckID{"quick", "quick", "signature"}, run)
		if err != nil {
			t.Fatalf("analyze: %v", err)
		}
		if calls != 2 || len(conclusion.Results) != 2 {
			t.Fatalf("calls=%d results=%d, want each check once", calls, len(conclusion.Results))
		}
	})
}

func TestStringersAndEmptyConclusion(t *testing.T) {
	if VerdictNegative.String() != "negative" || VerdictPositive.String() != "positive" {
		t.Fatal("verdict stringer mismatch")
	}
	if Verdict(99).String() != "unknown" {
		t.Fatal("unknown verdict stringer mismatch")
	}
	statuses := map[Status]string{
		StatusIncomplete: "incomplete",
		StatusClean:      "clean",
		StatusThreat:     "threat",
	}
	for status, want := range statuses {
		if status.String() != want {
			t.Fatalf("status %d string = %q, want %q", status, status.String(), want)
		}
	}
	if Status(99).String() != "unknown" {
		t.Fatal("unknown status stringer mismatch")
	}

	empty := &Conclusion{Key: testKey("v1")}
	if empty.Status() != StatusIncomplete {
		t.Fatal("conclusion with no executed checks must be incomplete")
	}
	if !empty.Covers(nil) {
		t.Fatal("an empty request set is trivially covered")
	}
}
