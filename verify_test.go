package poset

import (
	"fmt"
	"math/rand"
	"testing"
)

// Cross-check Match against the independent full-permutation enumeration
// on randomized posets with duplicate-heavy labels.
func TestRandomPosetsConsistentWithBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	alphabet := []string{"x", "y", "z"}

	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(7) // 1..7 events keeps n! brute force cheap
		c := NewCase(fmt.Sprintf("rand-%d", trial))

		// Random permutation defines a hidden order; edges only go from
		// earlier to later, guaranteeing acyclicity.
		perm := rng.Perm(n)
		ids := make([]EventID, n)
		for i := range ids {
			ids[i] = EventID(fmt.Sprintf("e%d", i))
		}
		for _, id := range ids {
			if err := c.AddEvent(id, alphabet[rng.Intn(len(alphabet))]); err != nil {
				t.Fatalf("AddEvent: %v", err)
			}
		}
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				if rng.Float64() < 0.3 {
					u, v := ids[perm[i]], ids[perm[j]]
					if err := c.AddPrecedence(u, v); err != nil {
						t.Fatalf("AddPrecedence %s -> %s: %v", u, v, err)
					}
				}
			}
		}

		k := 1 + rng.Intn(4)
		pat := make([]string, k)
		for i := range pat {
			pat[i] = alphabet[rng.Intn(len(alphabet))]
		}

		snap, err := c.Match(pat)
		if err != nil {
			t.Fatalf("trial %d: Match: %v", trial, err)
		}
		if err := c.ConsistentWithBruteForce(snap); err != nil {
			t.Fatalf("trial %d (n=%d, pattern=%v): %v", trial, n, pat, err)
		}
		for _, m := range snap.Matches {
			assertTopological(t, c, m.Witness)
		}
	}
}

// The retained closed search range must equal the brute-force set of
// topological orders when no match exists.
func TestClosedRangeEqualsBruteForceExtensions(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 100; trial++ {
		n := 2 + rng.Intn(5)
		c := NewCase(fmt.Sprintf("closed-%d", trial))
		perm := rng.Perm(n)
		ids := make([]EventID, n)
		for i := range ids {
			ids[i] = EventID(fmt.Sprintf("e%d", i))
		}
		for _, id := range ids {
			if err := c.AddEvent(id, "u"); err != nil {
				t.Fatalf("AddEvent: %v", err)
			}
		}
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				if rng.Float64() < 0.4 {
					if err := c.AddPrecedence(ids[perm[i]], ids[perm[j]]); err != nil {
						t.Fatalf("AddPrecedence: %v", err)
					}
				}
			}
		}
		// All labels are "u", so a pattern containing "v" can never match.
		snap, err := c.Match([]string{"u", "v"})
		if err != nil {
			t.Fatalf("Match: %v", err)
		}
		if snap.Matched() {
			t.Fatalf("trial %d: impossible pattern matched", trial)
		}
		_, explored := BruteForce(c.Events(), c.Edges(), snap.Pattern)
		if snap.Search.Explored != explored || uint64(len(snap.Search.Extensions)) != explored {
			t.Fatalf("trial %d: closed range %d/%d, brute force %d",
				trial, snap.Search.Explored, len(snap.Search.Extensions), explored)
		}
	}
}
