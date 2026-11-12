package taskpkg_test

import (
	"crypto/ed25519"
	"errors"
	"testing"

	"example.org/domainreissue/q023"
)

const (
	zoneA taskpkg.ZoneID = "zone-a"
	zoneB taskpkg.ZoneID = "zone-b"
)

func seedStore(t *testing.T, zone taskpkg.ZoneID, pub ed25519.PublicKey) *taskpkg.Store {
	t.Helper()
	return taskpkg.NewStore(zone, 1, []taskpkg.Record{
		{Key: "alpha", Value: []byte("v1")},
		{Key: "beta", Value: []byte("v1")},
		{Key: "gamma", Value: []byte("v1")},
	}, []ed25519.PublicKey{pub})
}

func TestApplyValidRound(t *testing.T) {
	pub, priv := mustKey(t)
	store := seedStore(t, zoneA, pub)

	beta, _ := store.Lookup("beta")
	updatedAlpha := taskpkg.RecordDigest("alpha", []byte("v2"))

	round := mustBuild(t, zoneA, 1, 2, priv,
		[]taskpkg.Operation{
			taskpkg.Upsert("alpha", []byte("v2")),
			taskpkg.Delete("beta", beta.Digest()),
		},
		[]taskpkg.Operation{
			taskpkg.Delete("alpha", updatedAlpha),
			taskpkg.Upsert("delta", []byte("born")),
		},
	)

	if err := store.Apply(round); err != nil {
		t.Fatalf("Apply valid round: %v", err)
	}

	if got := store.Version(); got != 2 {
		t.Fatalf("version = %d, want 2", got)
	}
	if _, ok := store.Lookup("alpha"); ok {
		t.Fatalf("alpha should have been deleted")
	}
	if _, ok := store.Lookup("beta"); ok {
		t.Fatalf("beta should have been deleted")
	}
	if rec, ok := store.Lookup("delta"); !ok || string(rec.Value) != "born" {
		t.Fatalf("delta = %v, %v", rec, ok)
	}
	if rec, ok := store.Lookup("gamma"); !ok || string(rec.Value) != "v1" {
		t.Fatalf("gamma should remain untouched, got %v, %v", rec, ok)
	}

	// Replaying the same complete round at the new version must fail.
	if err := store.Apply(round); !errors.Is(err, taskpkg.ErrVersionMismatch) {
		t.Fatalf("replay error = %v, want ErrVersionMismatch", err)
	}
}

func TestCrossZoneSplice(t *testing.T) {
	pubA, privA := mustKey(t)
	pubB, privB := mustKey(t)

	storeA := seedStore(t, zoneA, pubA)
	storeB := seedStore(t, zoneB, pubB)

	betaA, _ := storeA.Lookup("beta")

	roundA := mustBuild(t, zoneA, 1, 2, privA,
		[]taskpkg.Operation{taskpkg.Upsert("alpha", []byte("zone-a-v2"))},
		[]taskpkg.Operation{taskpkg.Upsert("gamma", []byte("zone-a-v2"))},
	)

	// A genuinely signed round for another zone containing a dangerous
	// delete. Every signature is valid; only the context is wrong.
	roundB := mustBuild(t, zoneB, 1, 2, privB,
		[]taskpkg.Operation{taskpkg.Delete("beta", betaA.Digest())},
	)

	before := snapshot(storeA, "alpha", "beta", "gamma")

	// 1. Submitting zone B's signed segment directly to zone A.
	assertRejected(t, storeA, roundB, taskpkg.ErrZoneMismatch)

	// 2. Splicing zone B's signed segment into zone A's chain. The digest
	// link breaks; relinking still leaves the zone binding intact, and no
	// already-signed segment is modified.
	spliced := []taskpkg.Segment{roundA[0]}
	foreign := roundB[0]
	foreign.Final = false
	foreign.Index = 1
	foreign.PrevDigest = roundA[0].Digest()
	spliced = append(spliced, foreign, roundA[1])

	assertRejected(t, storeA, spliced, taskpkg.ErrZoneMismatch)

	// 3. Rewriting the zone field cannot work: the signature covers it.
	forged := cloneSegments(spliced)
	forged[1].Zone = zoneA
	assertRejected(t, storeA, forged, taskpkg.ErrSignature)

	after := snapshot(storeA, "alpha", "beta", "gamma")
	if mapsUnequal(before, after) {
		t.Fatalf("cross-zone attack mutated records:\nbefore=%v\nafter =%v", before, after)
	}
	if storeB.Version() != 1 {
		t.Fatalf("unrelated store B version changed to %d", storeB.Version())
	}
}

func TestTruncation(t *testing.T) {
	pub, priv := mustKey(t)
	store := seedStore(t, zoneA, pub)

	beta, _ := store.Lookup("beta")
	round := mustBuild(t, zoneA, 1, 2, priv,
		[]taskpkg.Operation{taskpkg.Upsert("alpha", []byte("v2"))},
		[]taskpkg.Operation{taskpkg.Delete("beta", beta.Digest())},
	)

	before := snapshot(store, "alpha", "beta", "gamma")

	// Dropping the final segment leaves no final marker.
	assertRejected(t, store, round[:1], taskpkg.ErrFinalSegment)

	// Keeping only the final segment breaks index ordering.
	assertRejected(t, store, round[1:], taskpkg.ErrSegmentIndex)

	// A forged round digest with the real signatures still attached fails
	// because the final signature covers the round digest.
	tampered := cloneSegments(round)
	tampered[1].RoundDigest[0] ^= 0xFF
	assertRejected(t, store, tampered, taskpkg.ErrSignature)

	// Lying about the segment count fails because the signature covers it.
	lie := cloneSegments(round)
	lie[1].RoundSize = 3
	assertRejected(t, store, lie, taskpkg.ErrSignature)

	after := snapshot(store, "alpha", "beta", "gamma")
	if mapsUnequal(before, after) {
		t.Fatalf("truncation attack mutated records:\nbefore=%v\nafter =%v", before, after)
	}
}

func TestDuplicateSequence(t *testing.T) {
	pub, priv := mustKey(t)
	store := seedStore(t, zoneA, pub)

	round := mustBuild(t, zoneA, 1, 2, priv,
		[]taskpkg.Operation{taskpkg.Upsert("alpha", []byte("v2"))},
		[]taskpkg.Operation{taskpkg.Upsert("beta", []byte("v2"))},
	)

	before := snapshot(store, "alpha", "beta")

	dup := cloneSegments(round)
	dup[1].Index = 0
	assertRejected(t, store, dup, taskpkg.ErrSegmentIndex)

	// Reordering so indices no longer match positions is likewise rejected.
	reordered := cloneSegments(round)
	reordered[0], reordered[1] = reordered[1], reordered[0]
	assertRejected(t, store, reordered, taskpkg.ErrSegmentIndex)

	after := snapshot(store, "alpha", "beta")
	if mapsUnequal(before, after) {
		t.Fatalf("duplicate-sequence attack mutated records:\nbefore=%v\nafter =%v", before, after)
	}
}
