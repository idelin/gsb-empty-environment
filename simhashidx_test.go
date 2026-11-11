package simhashidx

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func TestSimHashKnownValues(t *testing.T) {
	// Empty input yields the all-zero fingerprint.
	if got := SimHash(nil); got != 0 {
		t.Fatalf("SimHash(nil) = %#x, want 0", got)
	}

	// SimHash is deterministic and order independent.
	features := []Feature{
		{Token: "crawler", Weight: 3},
		{Token: "pipeline", Weight: 2},
		{Token: "simhash", Weight: 5},
	}
	first := SimHash(features)
	shuffled := []Feature{features[2], features[0], features[1]}
	if got := SimHash(shuffled); got != first {
		t.Fatalf("SimHash changed with feature order: %#x != %#x", got, first)
	}

	// WeightedTokens must yield the same fingerprint as explicit weights.
	equivalent := SimHash([]Feature{
		{Token: "a", Weight: 3},
		{Token: "b", Weight: 2},
		{Token: "c", Weight: 1},
	})
	if got := SimHash(WeightedTokens([]string{"a", "b", "a", "c", "a", "b"})); got != equivalent {
		t.Fatalf("WeightedTokens SimHash = %#x, want %#x", got, equivalent)
	}

	// Hamming counts bit differences exactly.
	if got := Hamming(0, 0); got != 0 {
		t.Fatalf("Hamming(0,0) = %d, want 0", got)
	}
	if got := Hamming(^uint64(0), 0); got != 64 {
		t.Fatalf("Hamming(all ones, 0) = %d, want 64", got)
	}
}

func TestSimHashNearDuplicateDocuments(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 7))

	// Build a long base page, then change only two token occurrences. With
	// hundreds of weighted features the two fingerprints must stay very close.
	vocabulary := make([]string, 400)
	for i := range vocabulary {
		vocabulary[i] = "term-" + string(rune('a'+i/26)) + string(rune('a'+i%26))
	}
	base := make([]string, 300)
	for i := range base {
		base[i] = vocabulary[rng.IntN(len(vocabulary))]
	}
	nearDuplicate := slices.Clone(base)
	nearDuplicate[10] = "replacement-one"
	nearDuplicate[200] = "replacement-two"

	baseHash := SimHash(WeightedTokens(base))
	nearHash := SimHash(WeightedTokens(nearDuplicate))
	if distance := Hamming(baseHash, nearHash); distance > 16 {
		t.Fatalf("near-duplicate pages differ by %d bits, want <= 16", distance)
	}

	// An unrelated random page should be far away.
	unrelated := make([]string, 300)
	for i := range unrelated {
		unrelated[i] = vocabulary[rng.IntN(len(vocabulary))] + "-other"
	}
	if distance := Hamming(baseHash, SimHash(WeightedTokens(unrelated))); distance <= 16 {
		t.Fatalf("unrelated page is only %d bits away, want > 16", distance)
	}
}

// flipBits returns fingerprint with k distinct bit positions flipped.
func flipBits(rng *rand.Rand, fingerprint uint64, k int) uint64 {
	positions := rng.Perm(64)[:k]
	for _, position := range positions {
		fingerprint ^= uint64(1) << position
	}
	return fingerprint
}

func equalUint64Sets(t *testing.T, got, want []uint64, label string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s mismatch:\n got  %v\n want %v", label, got, want)
	}
}

func TestQueryDistanceBoundary(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	base := rng.Uint64()
	index := NewIndex()

	for distance := 0; distance <= 6; distance++ {
		index.Insert(flipBits(rng, base, distance))
	}

	got := index.Query(base)
	if len(got) != 4 { // distances 0..3
		t.Fatalf("Query returned %d fingerprints, want 4: %v", len(got), got)
	}
	for _, fingerprint := range got {
		if Hamming(base, fingerprint) > MaxHamming {
			t.Fatalf("Query returned fingerprint at distance %d > %d", Hamming(base, fingerprint), MaxHamming)
		}
	}
}

func TestRandomFingerprintsAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261111, 99))

	const fingerprintCount = 5000
	const queryCount = 2000

	index := NewIndex()
	stored := make([]uint64, 0, fingerprintCount)
	seen := make(map[uint64]struct{}, fingerprintCount)

	for len(stored) < fingerprintCount {
		fingerprint := rng.Uint64()
		if _, dup := seen[fingerprint]; dup {
			continue
		}
		seen[fingerprint] = struct{}{}
		stored = append(stored, fingerprint)
		index.Insert(fingerprint)
	}

	// Mixed workload: near neighbors (distance 0..3), just-too-far neighbors
	// (distance 4), and fully random probes.
	totalCandidates := 0
	for queryNumber := 0; queryNumber < queryCount; queryNumber++ {
		var queryFingerprint uint64
		switch queryNumber % 3 {
		case 0, 1:
			base := stored[rng.IntN(len(stored))]
			distance := rng.IntN(MaxHamming + 1) // 0..3: guaranteed matches
			queryFingerprint = flipBits(rng, base, distance)
		case 2:
			if rng.IntN(2) == 0 {
				queryFingerprint = flipBits(rng, stored[rng.IntN(len(stored))], MaxHamming+1)
			} else {
				queryFingerprint = rng.Uint64()
			}
		}

		got, candidates := index.QueryCandidates(queryFingerprint)
		totalCandidates += candidates
		want := BruteForce(stored, queryFingerprint, MaxHamming)
		equalUint64Sets(t, got, want, "index vs brute force")
	}

	// Delete half of the fingerprints and re-verify every query. Deleted
	// fingerprints must never appear in index results.
	deleted := make(map[uint64]struct{}, fingerprintCount/2)
	live := make([]uint64, 0, fingerprintCount)
	rng.Shuffle(len(stored), func(i, j int) { stored[i], stored[j] = stored[j], stored[i] })
	for i, fingerprint := range stored {
		if i < fingerprintCount/2 {
			if !index.Delete(fingerprint) {
				t.Fatalf("Delete(%#x) reported missing fingerprint", fingerprint)
			}
			deleted[fingerprint] = struct{}{}
		} else {
			live = append(live, fingerprint)
		}
	}

	if index.Len() != len(live) {
		t.Fatalf("Len after deletes = %d, want %d", index.Len(), len(live))
	}

	// Deleting twice must fail, and deleting an unknown fingerprint must fail.
	for fingerprint := range deleted {
		if index.Delete(fingerprint) {
			t.Fatalf("second Delete(%#x) unexpectedly succeeded", fingerprint)
		}
		break
	}
	if index.Delete(rng.Uint64()) {
		t.Fatal("Delete of an unknown fingerprint unexpectedly succeeded")
	}

	for queryNumber := 0; queryNumber < queryCount; queryNumber++ {
		queryFingerprint := flipBits(rng, live[rng.IntN(len(live))], rng.IntN(MaxHamming+1))
		got, candidates := index.QueryCandidates(queryFingerprint)
		totalCandidates += candidates

		want := BruteForce(live, queryFingerprint, MaxHamming)
		equalUint64Sets(t, got, want, "post-delete index vs brute force")

		for _, fingerprint := range got {
			if _, isDeleted := deleted[fingerprint]; isDeleted {
				t.Fatalf("query returned deleted fingerprint %#x", fingerprint)
			}
		}
	}

	stats := index.Stats()
	if stats.TableCount != tableCount {
		t.Fatalf("Stats.TableCount = %d, want %d", stats.TableCount, tableCount)
	}
	if stats.BucketCount == 0 || stats.Occurrences != len(live)*tableCount {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	t.Logf("stored=%d live=%d queries=%d total_candidates=%d avg_candidates=%.2f tables=%d buckets=%d brute_scan_per_query=%d",
		fingerprintCount, len(live), queryCount*2, totalCandidates,
		float64(totalCandidates)/float64(queryCount*2),
		stats.TableCount, stats.BucketCount, len(live))
}

func TestDuplicateInsertionAndDeletion(t *testing.T) {
	index := NewIndex()
	fingerprint := uint64(0xdeadbeefcafef00d)

	index.Insert(fingerprint)
	index.Insert(fingerprint)
	index.Insert(fingerprint)
	if index.Len() != 3 {
		t.Fatalf("Len = %d, want 3", index.Len())
	}

	// Duplicate occurrences collapse into one distinct query result.
	if got := index.Query(fingerprint); len(got) != 1 || got[0] != fingerprint {
		t.Fatalf("Query = %v, want single %#x", got, fingerprint)
	}

	for want := 2; want >= 0; want-- {
		if !index.Delete(fingerprint) {
			t.Fatal("Delete failed while occurrences remained")
		}
		if index.Len() != want {
			t.Fatalf("Len = %d, want %d", index.Len(), want)
		}
	}
	if len(index.Query(fingerprint)) != 0 {
		t.Fatal("Query returned fingerprint after all occurrences were deleted")
	}
	if stats := index.Stats(); stats.BucketCount != 0 || stats.Occurrences != 0 {
		t.Fatalf("empty index retains buckets: %+v", stats)
	}
}
