package queryplan

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestReplaceDelegationRejectsStaleGeneration ensures a referral or host call
// can never roll the delegation clock backward.
func TestReplaceDelegationRejectsStaleGeneration(t *testing.T) {
	planner, _ := testPlanner(t, delegation(1, "a"), time.Second)

	if err := planner.ReplaceDelegation(delegation(1, "a")); err == nil {
		t.Fatal("equal generation accepted, want error")
	}
	if err := planner.ReplaceDelegation(delegation(0, "a")); err == nil {
		t.Fatal("older generation accepted, want error")
	}
	if err := planner.ReplaceDelegation(delegation(2, "b")); err != nil {
		t.Fatalf("newer generation rejected: %v", err)
	}
	if got := planner.CurrentGeneration(); got != 2 {
		t.Fatalf("generation = %d, want 2", got)
	}
}

// TestCachesAreIsolatedByGeneration proves each generation owns its cache
// entries: an answer written under gen 1 is invisible to a gen 2 plan and
// vice versa, so cache reads can never cross delegation boundaries.
func TestCachesAreIsolatedByGeneration(t *testing.T) {
	planner, upstream := testPlanner(t, delegation(1, "a"), time.Second)
	q := Query{Name: "iso.test", QType: 1}

	first, err := planner.Lookup(context.Background(), q)
	if err != nil {
		t.Fatalf("gen-1 lookup: %v", err)
	}
	if first.Generation != 1 || first.FromCache {
		t.Fatalf("unexpected first result: %+v", first)
	}

	// Gen-1 repeat is a cache hit with no extra upstream traffic.
	before := len(upstream.Calls())
	if r, err := planner.Lookup(context.Background(), q); err != nil || !r.FromCache {
		t.Fatalf("gen-1 repeat should hit cache: %+v %v", r, err)
	}
	if len(upstream.Calls()) != before {
		t.Fatal("gen-1 cache hit issued an upstream call")
	}

	if err := planner.ReplaceDelegation(delegation(2, "b")); err != nil {
		t.Fatalf("replace: %v", err)
	}

	// The gen-1 entry must not serve a gen-2 plan.
	second, err := planner.Lookup(context.Background(), q)
	if err != nil {
		t.Fatalf("gen-2 lookup: %v", err)
	}
	if second.Generation != 2 || second.FromCache {
		t.Fatalf("gen-2 plan must not read the gen-1 cache: %+v", second)
	}

	// Reverting is rejected, so a stale in-flight plan cannot reinstall gen 1.
	if err := planner.ReplaceDelegation(delegation(1, "a")); err == nil {
		t.Fatal("stale rollback accepted")
	}
}

// TestReferralIsGenerationGated checks that a NewDelegation referral is only
// installed while the answering plan still matches the current generation.
func TestReferralIsGenerationGated(t *testing.T) {
	t.Run("current generation installs referral", func(t *testing.T) {
		planner, upstream := testPlanner(t, delegation(1, "a"), time.Second)
		upstream.scriptCandidate("a", &Response{
			Answer:        "referred",
			NewDelegation: ptrDelegation(delegation(2, "n1", "n2")),
		}, nil)

		if _, err := planner.Lookup(context.Background(), Query{Name: "ref.test", QType: 1}); err != nil {
			t.Fatalf("lookup: %v", err)
		}
		if got := planner.CurrentGeneration(); got != 2 {
			t.Fatalf("generation after referral = %d, want 2", got)
		}
	})

	t.Run("stale generation referral is ignored", func(t *testing.T) {
		planner, upstream := testPlanner(t, delegation(1, "a"), time.Second)
		upstream.scriptCandidate("a", &Response{
			Answer:        "late-referral",
			NewDelegation: ptrDelegation(delegation(2, "stale")),
		}, nil)
		upstream.PauseGeneration(1)

		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = planner.Lookup(context.Background(), Query{Name: "ref.test", QType: 1})
		}()

		upstream.waitBlocked(t, 1)
		// Host moves to a newer generation while the answer is parked.
		if err := planner.ReplaceDelegation(delegation(3, "fresh")); err != nil {
			t.Fatalf("replace: %v", err)
		}
		upstream.ReleaseAll()
		<-done

		if got := planner.CurrentGeneration(); got != 3 {
			t.Fatalf("stale referral changed generation to %d, want 3", got)
		}
		// The late answer must not have cached under either generation.
		r, err := planner.Lookup(context.Background(), Query{Name: "ref.test", QType: 1})
		if err != nil {
			t.Fatalf("lookup: %v", err)
		}
		if r.FromCache {
			t.Fatal("late stale answer was cached")
		}
	})
}

// TestPreCanceledContextReturnsWithoutGoroutines makes sure a context already
// canceled before Lookup creates no plan at all.
func TestPreCanceledContextReturnsWithoutGoroutines(t *testing.T) {
	planner, _ := testPlanner(t, delegation(1, "a"), time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := planner.Lookup(ctx, Query{Name: "x", QType: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := planner.ActivePlans(); got != 0 {
		t.Fatalf("active plans = %d, want 0", got)
	}
	assertNoPackageGoroutines(t)
}

func ptrDelegation(d Delegation) *Delegation { return &d }
