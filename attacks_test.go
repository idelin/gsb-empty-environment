package taskpkg_test

import (
	"errors"
	"sync"
	"testing"

	"example.org/domainreissue/q023"
)

func TestConcurrentRounds(t *testing.T) {
	pub, priv := mustKey(t)
	store := seedStore(t, zoneA, pub)

	alpha, _ := store.Lookup("alpha")
	beta, _ := store.Lookup("beta")

	// Two rounds both correctly built against version 1. Only one can win.
	roundOne := mustBuild(t, zoneA, 1, 2, priv,
		[]taskpkg.Operation{
			taskpkg.Upsert("alpha", []byte("from-round-one")),
			taskpkg.Delete("beta", beta.Digest()),
		},
	)
	roundTwo := mustBuild(t, zoneA, 1, 2, priv,
		[]taskpkg.Operation{
			taskpkg.Upsert("gamma", []byte("from-round-two")),
			taskpkg.Delete("alpha", alpha.Digest()),
		},
	)

	const workers = 32
	var wg sync.WaitGroup
	var mu sync.Mutex
	var accepted, rejected int64

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				err = store.Apply(roundOne)
			} else {
				err = store.Apply(roundTwo)
			}
			mu.Lock()
			switch {
			case err == nil:
				accepted++
			case errors.Is(err, taskpkg.ErrVersionMismatch):
				rejected++
			default:
				t.Errorf("unexpected Apply error: %v", err)
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	if accepted != 1 {
		t.Fatalf("accepted = %d, want exactly 1; rejected = %d", accepted, rejected)
	}
	if rejected != workers-1 {
		t.Fatalf("rejected = %d, want %d", rejected, workers-1)
	}
	if store.Version() != 2 {
		t.Fatalf("version = %d, want 2", store.Version())
	}

	// A follow-up based on the new version still commits, proving the store
	// stays live after the contention.
	later := mustBuild(t, zoneA, 2, 3, priv,
		[]taskpkg.Operation{taskpkg.Upsert("survivor", []byte("ok"))},
	)
	if err := store.Apply(later); err != nil {
		t.Fatalf("follow-up round rejected: %v", err)
	}
	if store.Version() != 3 {
		t.Fatalf("version = %d, want 3", store.Version())
	}
}

func TestFailureNeverDeletesRecords(t *testing.T) {
	pub, priv := mustKey(t)
	store := seedStore(t, zoneA, pub)

	// The final delete references a stale digest, so the whole round is
	// rejected and its earlier upserts must never become visible.
	round := mustBuild(t, zoneA, 1, 2, priv,
		[]taskpkg.Operation{taskpkg.Upsert("alpha", []byte("must-not-commit"))},
		[]taskpkg.Operation{
			taskpkg.Upsert("delta", []byte("also-must-not-commit")),
			taskpkg.Delete("beta", taskpkg.RecordDigest("beta", []byte("stale-digest"))),
		},
	)

	before := snapshot(store, "alpha", "beta", "gamma", "delta")
	assertRejected(t, store, round, taskpkg.ErrDeleteMismatch)
	after := snapshot(store, "alpha", "beta", "gamma", "delta")
	if mapsUnequal(before, after) {
		t.Fatalf("failed round mutated records:\nbefore=%v\nafter =%v", before, after)
	}
	if store.Version() != 1 {
		t.Fatalf("version = %d, want 1", store.Version())
	}

	// A delete for a key that was never present is rejected the same way.
	missing := mustBuild(t, zoneA, 1, 2, priv,
		[]taskpkg.Operation{taskpkg.Delete("ghost", taskpkg.RecordDigest("ghost", nil))},
	)
	assertRejected(t, store, missing, taskpkg.ErrDeleteMismatch)
	afterMissing := snapshot(store, "alpha", "beta", "gamma", "delta")
	if mapsUnequal(before, afterMissing) {
		t.Fatalf("missing-key delete mutated records: %v", afterMissing)
	}
}

func TestSpliceFromAnotherRoundSameZone(t *testing.T) {
	pub, priv := mustKey(t)
	store := seedStore(t, zoneA, pub)

	gamma, _ := store.Lookup("gamma")

	roundOne := mustBuild(t, zoneA, 1, 2, priv,
		[]taskpkg.Operation{taskpkg.Upsert("alpha", []byte("round-one"))},
		[]taskpkg.Operation{taskpkg.Upsert("beta", []byte("round-one"))},
	)
	roundTwo := mustBuild(t, zoneA, 1, 2, priv,
		[]taskpkg.Operation{taskpkg.Delete("gamma", gamma.Digest())},
	)

	before := snapshot(store, "alpha", "beta", "gamma")

	// The foreign segment has a valid signature for the same zone and
	// versions, but its body digest and predecessor do not belong to the
	// target chain.
	spliced := cloneSegments(roundOne)
	foreign := roundTwo[0]
	foreign.Index = 2
	foreign.PrevDigest = roundOne[1].Digest()
	spliced = append(spliced, foreign)

	assertRejected(t, store, spliced, taskpkg.ErrFinalSegment)

	// Marking the chain final correctly still fails at signature/round
	// level because the round digest no longer matches the final segment.
	relinked := cloneSegments(spliced)
	relinked[1].Final = false
	relinked[2].Final = true
	assertRejected(t, store, relinked, taskpkg.ErrSignature)

	after := snapshot(store, "alpha", "beta", "gamma")
	if mapsUnequal(before, after) {
		t.Fatalf("cross-round splice mutated records:\nbefore=%v\nafter =%v", before, after)
	}
}

func TestUnauthorizedAndTamperedContext(t *testing.T) {
	pub, priv := mustKey(t)
	_, strangerPriv := mustKey(t)
	store := seedStore(t, zoneA, pub)

	round := mustBuild(t, zoneA, 1, 2, priv,
		[]taskpkg.Operation{taskpkg.Upsert("alpha", []byte("ok"))},
	)

	// A perfectly formed signature from a key the zone does not trust.
	strangerRound := mustBuild(t, zoneA, 1, 2, strangerPriv,
		[]taskpkg.Operation{taskpkg.Upsert("alpha", []byte("ok"))},
	)
	assertRejected(t, store, strangerRound, taskpkg.ErrSignature)

	before := snapshot(store, "alpha", "beta", "gamma")

	// Tampering with any context-bound field must reject the round. Fields
	// that form an independently checked context (version, chain link) are
	// caught by that check; payload tampering invalidates the signature. In
	// every case the original signature cannot authorize the new context.
	cases := map[string]struct {
		mutate func([]taskpkg.Segment)
		want   error
	}{
		"target version": {func(segs []taskpkg.Segment) { segs[0].TargetVersion = 99 }, taskpkg.ErrSignature},
		"start version":  {func(segs []taskpkg.Segment) { segs[0].StartVersion = 0 }, taskpkg.ErrVersionMismatch},
		"operation":      {func(segs []taskpkg.Segment) { segs[0].Operations[0].Value = []byte("tampered") }, taskpkg.ErrSignature},
		"predecessor":    {func(segs []taskpkg.Segment) { segs[0].PrevDigest[0] = 1 }, taskpkg.ErrChainLink},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tampered := cloneSegments(round)
			tc.mutate(tampered)
			assertRejected(t, store, tampered, tc.want)
		})
	}

	// Starting at the wrong version is a context rejection independent of
	// the signature being valid.
	stale := mustBuild(t, zoneA, 0, 1, priv,
		[]taskpkg.Operation{taskpkg.Upsert("alpha", []byte("old"))},
	)
	assertRejected(t, store, stale, taskpkg.ErrVersionMismatch)

	after := snapshot(store, "alpha", "beta", "gamma")
	if mapsUnequal(before, after) {
		t.Fatalf("tamper attempts mutated records:\nbefore=%v\nafter =%v", before, after)
	}

	// The untouched, legitimate round still applies.
	if err := store.Apply(round); err != nil {
		t.Fatalf("legitimate round rejected: %v", err)
	}
}
