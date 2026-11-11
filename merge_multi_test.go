package netsec

import (
	"math/rand"
	"testing"
)

// TestMergeMultiScopeRandomized builds a collection spanning several users,
// windows and groups, scatters it across shards with duplicate deliveries and
// withdrawals, and compares the merged trim against a single-host reference
// across many random assignments.
func TestMergeMultiScopeRandomized(t *testing.T) {
	const localK, globalCap = 2, 4

	var records []Record
	id := 0
	for _, user := range []string{"u1", "u2", "u3"} {
		for _, window := range []int64{10, 20} {
			for _, group := range []string{"gA", "gB", "gC"} {
				for n := 0; n < 3; n++ {
					id++
					records = append(records, rec("ev"+itoo(id), user, group, window, "body"))
				}
			}
		}
	}

	withdrawEvery := 7

	rng := rand.New(rand.NewSource(777))
	for iter := 0; iter < 40; iter++ {
		ref, err := NewSelector(testKey, localK, globalCap)
		if err != nil {
			t.Fatal(err)
		}

		shuffled := append([]Record(nil), records...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

		const shardCount = 5
		shards := make([]*Selector, shardCount)
		for i := range shards {
			shards[i], err = NewSelector(testKey, localK, globalCap)
			if err != nil {
				t.Fatal(err)
			}
		}

		for i, r := range shuffled {
			if err := ref.Ingest(r); err != nil {
				t.Fatalf("reference Ingest: %v", err)
			}
			target := shards[rng.Intn(shardCount)]
			if err := target.Ingest(r); err != nil {
				t.Fatalf("shard Ingest: %v", err)
			}

			// Roughly one third of records are retransmitted to another shard.
			if i%3 == 0 {
				other := shards[rng.Intn(shardCount)]
				if err := other.Ingest(r); err != nil {
					t.Fatalf("retransmit: %v", err)
				}
			}

			if i%withdrawEvery == 0 {
				ref.Withdraw(r.EventID)
				shards[rng.Intn(shardCount)].Withdraw(r.EventID)
			}
		}

		merged, err := Merge(shards...)
		if err != nil {
			t.Fatalf("Merge iter %d: %v", iter, err)
		}

		want := ref.Trim()
		got := merged.Trim()
		if !sameResult(got, want) {
			t.Fatalf("iter %d mismatch:\n merged = %+v\n want   = %+v", iter, got, want)
		}

		// Every record must be accounted for exactly once.
		if accounted := len(got.Kept) + len(got.Dropped) + len(got.Withdrawn); accounted != len(records) {
			t.Fatalf("iter %d accounting: %d of %d", iter, accounted, len(records))
		}

		// Global cap holds per (user, window).
		counts := map[string]int{}
		for _, r := range got.Kept {
			counts[r.UserID+":"+itoo64(r.Window)]++
		}
		for scope, count := range counts {
			if count > globalCap {
				t.Fatalf("iter %d global cap violated for %s: %d", iter, scope, count)
			}
		}
	}
}

func itoo(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}

func itoo64(n int64) string {
	return itoo(int(n))
}
