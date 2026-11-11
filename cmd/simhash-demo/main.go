// Command simhash-demo demonstrates the SimHash near-duplicate index on a small
// crawler corpus and on a large batch of random fingerprints, printing table
// and candidate statistics for both workloads.
package main

import (
	"fmt"
	"math/rand/v2"
	"strings"

	"example.org/simhashidx"
)

func tokenize(text string) []string {
	return strings.Fields(strings.ToLower(text))
}

type page struct {
	name string
	text string
}

func main() {
	demoCorpus()
	fmt.Println()
	demoRandomBenchmark()
}

func demoCorpus() {
	// Long pages with many repeated terms: replacing one low-impact word only
	// moves the SimHash by a few bits, which is the realistic near-duplicate
	// regime the index is built for.
	sharedBody := strings.Repeat("crawler pipeline simhash fingerprint hamming distance duplicate filter webpage content index bucket ", 4)
	originalText := sharedBody + "golang service harvests urls and stores weighted token features nightly"
	// Dropping one unique trailing sentence moves only a couple of hash bits.
	nearDuplicateText := sharedBody
	unrelatedText := strings.Repeat("database engine vectorized column storage parallel query execution planner transaction wal checkpoint ", 4) +
		"rust runtime schedules async tasks across worker threads and io rings"

	pages := []page{
		{name: "original", text: originalText},
		{name: "near-duplicate", text: nearDuplicateText},
		{name: "unrelated", text: unrelatedText},
	}

	index := simhashidx.NewIndex()
	hashes := make(map[string]uint64, len(pages))
	for _, current := range pages {
		fingerprint := simhashidx.SimHash(simhashidx.WeightedTokens(tokenize(current.text)))
		hashes[current.name] = fingerprint
		index.Insert(fingerprint)
		fmt.Printf("indexed %-15s fingerprint=%016x\n", current.name, fingerprint)
	}

	stats := index.Stats()
	fmt.Printf("\ntables=%d non_empty_buckets=%d stored_occurrences=%d\n",
		stats.TableCount, stats.BucketCount, stats.Fingerprints)

	query := hashes["original"]
	matches, candidates := index.QueryCandidates(query)
	fmt.Printf("\nquery=original candidates_inspected=%d matches=%d\n", candidates, len(matches))
	fmt.Printf("distance(original, near-duplicate)=%d distance(original, unrelated)=%d\n",
		simhashidx.Hamming(hashes["original"], hashes["near-duplicate"]),
		simhashidx.Hamming(hashes["original"], hashes["unrelated"]))

	nameByHash := make(map[uint64]string, len(hashes))
	for name, fingerprint := range hashes {
		nameByHash[fingerprint] = name
	}
	for _, fingerprint := range matches {
		fmt.Printf("  match page=%-15s hamming=%d\n",
			nameByHash[fingerprint], simhashidx.Hamming(query, fingerprint))
	}
	if simhashidx.Hamming(hashes["original"], hashes["near-duplicate"]) > simhashidx.MaxHamming {
		panic("demo corpus failed: near-duplicate page is not within the query radius")
	}
	if len(matches) != 2 || simhashidx.Hamming(hashes["original"], hashes["unrelated"]) <= simhashidx.MaxHamming {
		panic(fmt.Sprintf("demo corpus failed: expected original + near-duplicate only, got %d matches", len(matches)))
	}

	index.Delete(hashes["near-duplicate"])
	afterDelete := index.Query(query)
	fmt.Printf("\nafter deleting near-duplicate: %d match(es) remain\n", len(afterDelete))
	for _, fingerprint := range afterDelete {
		fmt.Printf("  match page=%-15s hamming=%d\n",
			nameByHash[fingerprint], simhashidx.Hamming(query, fingerprint))
	}
}

func demoRandomBenchmark() {
	rng := rand.New(rand.NewPCG(20261111, 20261111))

	const fingerprintCount = 10000
	const queryCount = 1000

	index := simhashidx.NewIndex()
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

	stats := index.Stats()
	totalCandidates := 0
	mismatches := 0
	totalMatches := 0

	for range queryCount {
		// Half the probes are near neighbors of stored fingerprints so that
		// positive matches are exercised; the rest are fully random.
		var query uint64
		if rng.IntN(2) == 0 {
			base := stored[rng.IntN(len(stored))]
			query = base ^ (uint64(1) << rng.IntN(64))
			if rng.IntN(2) == 0 {
				query ^= uint64(1) << rng.IntN(64)
			}
		} else {
			query = rng.Uint64()
		}

		got, candidates := index.QueryCandidates(query)
		want := simhashidx.BruteForce(stored, query, simhashidx.MaxHamming)
		if len(got) != len(want) {
			mismatches++
		}
		totalCandidates += candidates
		totalMatches += len(got)
	}

	fmt.Println("random-fingerprint differential benchmark")
	fmt.Printf("  stored_fingerprints : %d\n", fingerprintCount)
	fmt.Printf("  queries             : %d\n", queryCount)
	fmt.Printf("  permutation tables  : %d\n", stats.TableCount)
	fmt.Printf("  non-empty buckets   : %d (%.1f per table)\n",
		stats.BucketCount, float64(stats.BucketCount)/float64(stats.TableCount))
	fmt.Printf("  total candidates    : %d\n", totalCandidates)
	fmt.Printf("  avg candidates/query: %.2f vs brute-force scan %d entries/query\n",
		float64(totalCandidates)/queryCount, fingerprintCount)
	fmt.Printf("  total matches       : %d\n", totalMatches)
	fmt.Printf("  brute-force mismatches: %d\n", mismatches)

	// Delete every fingerprint and confirm the tables collapse back to empty.
	for _, fingerprint := range stored {
		index.Delete(fingerprint)
	}
	empty := index.Stats()
	fmt.Printf("  after full delete   : buckets=%d occurrences=%d len=%d\n",
		empty.BucketCount, empty.Occurrences, index.Len())
}
