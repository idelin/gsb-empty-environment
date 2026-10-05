package fuzzy

import (
	"math/rand"
	"testing"
)

func build(words ...string) *Dictionary {
	d := New()
	for _, w := range words {
		d.Insert(w)
	}
	return d
}

func checkHits(t *testing.T, res SearchResult, want []Hit) {
	t.Helper()
	if len(res.Hits) != len(want) {
		t.Fatalf("got %d hits %v, want %d %v", len(res.Hits), res.Hits, len(want), want)
	}
	for i, h := range res.Hits {
		if h != want[i] {
			t.Fatalf("hit %d = %v, want %v (all hits: %v)", i, h, want[i], res.Hits)
		}
	}
}

func TestSearchOrder(t *testing.T) {
	d := build("abc", "ab", "abd", "axy", "xyz")
	res := d.Search("abc", 4, Unlimited, unit)
	want := []Hit{
		{Word: "abc", Cost: 0},
		{Word: "ab", Cost: 1},
		{Word: "abd", Cost: 1},
		{Word: "axy", Cost: 2},
	}
	checkHits(t, res, want)
	if res.Proven != len(want) {
		t.Fatalf("Proven = %d, want %d with unlimited budget", res.Proven, len(want))
	}
}

func TestSearchLexicographicTieBreak(t *testing.T) {
	d := build("bb", "aa", "cc")
	res := d.Search("dd", 3, Unlimited, unit)
	want := []Hit{{"aa", 2}, {"bb", 2}, {"cc", 2}}
	checkHits(t, res, want)
}

// Asking for more words than the dictionary holds returns every word,
// all of them proven.
func TestSearchKExceedsWordCount(t *testing.T) {
	d := build("one", "two", "three")
	res := d.Search("one", 10, Unlimited, unit)
	if len(res.Hits) != 3 {
		t.Fatalf("got %d hits, want 3: %v", len(res.Hits), res.Hits)
	}
	if res.Proven != 3 {
		t.Fatalf("Proven = %d, want 3", res.Proven)
	}
}

// A word stored once is returned once, even though many edit paths lead
// from the query to it.
func TestSearchDistinctWords(t *testing.T) {
	d := build("ba", "ba", "ba")
	if d.Len() != 1 {
		t.Fatalf("Len = %d, want 1", d.Len())
	}
	c := Costs{Ins: 1, Del: 1, Rep: 2, Trans: 3}
	res := d.Search("ab", 5, Unlimited, c)
	checkHits(t, res, []Hit{{"ba", 2}}) // min of swap 3, delete+insert 2, replace 4
	if res.Proven != 1 {
		t.Fatalf("Proven = %d, want 1", res.Proven)
	}
}

func TestSearchEmptyAndZero(t *testing.T) {
	d := build()
	if res := d.Search("abc", 3, Unlimited, unit); len(res.Hits) != 0 || res.Proven != 0 {
		t.Fatalf("empty dictionary: got %+v", res)
	}
	d = build("a", "b")
	if res := d.Search("a", 0, Unlimited, unit); len(res.Hits) != 0 {
		t.Fatalf("k=0: got %+v", res)
	}
	// The empty word is a regular dictionary entry.
	d = build("", "abc")
	res := d.Search("abc", 2, Unlimited, unit)
	checkHits(t, res, []Hit{{"abc", 0}, {"", 3}})
}

// With maxCost = 1 the search cannot afford to prove anything costing 2
// or more: the "axy" hit surfaces during exploration but its rank is not
// certified, because pruned prefixes (bound 2) might hide better words.
func TestSearchBudgetExhaustedMarksProven(t *testing.T) {
	d := build("abc", "ab", "abd", "axy", "xyz")
	res := d.Search("abc", 4, 1, unit)
	want := []Hit{
		{Word: "abc", Cost: 0},
		{Word: "ab", Cost: 1},
		{Word: "abd", Cost: 1},
		{Word: "axy", Cost: 2},
	}
	checkHits(t, res, want)
	if res.Proven != 3 {
		t.Fatalf("Proven = %d, want 3 (only costs < 2 are certified by budget 1)", res.Proven)
	}
}

// A budget of zero still proves an exact match, but the second hit is
// not certified: the pruned "x" prefix (bound 1) might hide a word that
// ties or beats it.
func TestSearchZeroBudget(t *testing.T) {
	d := build("abc", "abd", "xyz")
	res := d.Search("abc", 2, 0, unit)
	checkHits(t, res, []Hit{{"abc", 0}, {"abd", 1}})
	if res.Proven != 1 {
		t.Fatalf("Proven = %d, want 1", res.Proven)
	}
}

// Cross-check searches against brute-force ranking over random small
// dictionaries, random budgets included.
func TestSearchAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	alphabet := []rune("ab")
	budgets := []int{0, 1, 2, 3, 4, 5, Unlimited}
	for iter := 0; iter < 400; iter++ {
		d := New()
		seen := map[string]bool{}
		var words []string
		for len(words) < 1+rng.Intn(40) {
			w := randWord(rng, alphabet, 5)
			if !seen[w] {
				seen[w] = true
				words = append(words, w)
				d.Insert(w)
			}
		}
		query := randWord(rng, alphabet, 5)
		k := rng.Intn(8)
		maxCost := budgets[rng.Intn(len(budgets))]
		c := randCosts(rng, 3)

		res := d.Search(query, k, maxCost, c)
		brute := bruteTopK(words, query, k, c)

		if len(res.Hits) > k {
			t.Fatalf("iter %d: more than k=%d hits: %v", iter, k, res.Hits)
		}
		if res.Proven < 0 || res.Proven > len(res.Hits) {
			t.Fatalf("iter %d: Proven = %d out of range (%d hits)", iter, res.Proven, len(res.Hits))
		}
		prev := Hit{Cost: -1}
		for i, h := range res.Hits {
			if i > 0 && (h.Cost < prev.Cost || (h.Cost == prev.Cost && h.Word <= prev.Word)) {
				t.Fatalf("iter %d: hits not ordered at %d: %v", iter, i, res.Hits)
			}
			if got := bruteDistance(query, h.Word, c); got != h.Cost {
				t.Fatalf("iter %d: hit %v has wrong cost, brute force says %d", iter, h, got)
			}
			prev = h
		}
		// The proven prefix must match the true ranking exactly.
		for i := 0; i < res.Proven; i++ {
			if i >= len(brute) || res.Hits[i] != brute[i] {
				t.Fatalf("iter %d: proven hit %d = %v, brute force says %v (query %q, k %d, budget %d, costs %+v)",
					iter, i, res.Hits[i], brute[i], query, k, maxCost, c)
			}
		}
		if maxCost == Unlimited {
			// With no budget limit the full answer is exact and proven.
			if res.Proven != len(res.Hits) {
				t.Fatalf("iter %d: Proven = %d, want %d with unlimited budget", iter, res.Proven, len(res.Hits))
			}
			if len(res.Hits) != len(brute) {
				t.Fatalf("iter %d: got %d hits, brute force has %d", iter, len(res.Hits), len(brute))
			}
			for i := range brute {
				if res.Hits[i] != brute[i] {
					t.Fatalf("iter %d: hit %d = %v, brute force says %v", iter, i, res.Hits[i], brute[i])
				}
			}
		}
	}
}
