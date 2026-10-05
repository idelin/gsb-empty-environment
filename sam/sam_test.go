package sam

import (
	"fmt"
	"testing"
)

// reference maps every non-empty substring of s to its sorted start
// positions, computed by brute force.
func reference(s string) map[string][]int {
	ref := make(map[string][]int)
	for i := 0; i < len(s); i++ {
		for j := i + 1; j <= len(s); j++ {
			sub := s[i:j]
			ref[sub] = append(ref[sub], i)
		}
	}
	return ref
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// checkAgainst verifies the automaton against the brute-force
// reference for s: distinct-substring count, occurrence positions of
// every substring, and absence of every non-substring up to length
// len(s)+1 over the alphabet.
func checkAgainst(t *testing.T, a *Automaton, s string, alphabet string) {
	t.Helper()
	if a.Len() != len(s) {
		t.Fatalf("Len() = %d, want %d", a.Len(), len(s))
	}
	ref := reference(s)
	if got := a.DistinctSubstrings(); got != len(ref) {
		t.Fatalf("text %q: DistinctSubstrings() = %d, want %d", s, got, len(ref))
	}
	for sub, want := range ref {
		got := a.Occurrences(sub)
		if !equalInts(got, want) {
			t.Fatalf("text %q: Occurrences(%q) = %v, want %v", s, sub, got, want)
		}
		if c := a.Count(sub); c != len(want) {
			t.Fatalf("text %q: Count(%q) = %d, want %d", s, sub, c, len(want))
		}
		if !a.Contains(sub) {
			t.Fatalf("text %q: Contains(%q) = false", s, sub)
		}
	}
	// Non-substrings must not be found: enumerate candidates over the
	// alphabet up to length len(s)+1 (bounded for small alphabets).
	var gen func(cur string, maxLen int)
	gen = func(cur string, maxLen int) {
		if len(cur) > 0 {
			if _, ok := ref[cur]; !ok {
				if a.Contains(cur) {
					t.Fatalf("text %q: Contains(%q) = true, want false", s, cur)
				}
				if got := a.Occurrences(cur); got != nil {
					t.Fatalf("text %q: Occurrences(%q) = %v, want nil", s, cur, got)
				}
			}
		}
		if len(cur) == maxLen {
			return
		}
		for i := 0; i < len(alphabet); i++ {
			gen(cur+string(alphabet[i]), maxLen)
		}
	}
	if len(alphabet) <= 3 && len(s) <= 9 {
		gen("", len(s)+1)
	}
}

func build(s string) *Automaton {
	a := New()
	a.AppendString(s)
	return a
}

func TestSmallStringsAgainstReference(t *testing.T) {
	cases := []struct {
		text     string
		alphabet string
	}{
		{"", "ab"},
		{"a", "ab"},
		{"aaaaaa", "ab"},          // repeated character
		{"abababab", "ab"},        // alternating
		{"aabbaabb", "ab"},        // repeated blocks
		{"mississippi", "misp"},   // classic clone-heavy case
		{"abcabcabc", "abc"},      // periodic over 3 symbols
		{"abacabadabacaba", "abc"},// nested clones
		{"zyxwvuts", "stuvwxyz"},  // no repetitions
	}
	for _, tc := range cases {
		checkAgainst(t, build(tc.text), tc.text, tc.alphabet)
	}
}

// TestExhaustiveBinary checks every binary string up to length 8.
func TestExhaustiveBinary(t *testing.T) {
	for n := 0; n <= 8; n++ {
		for mask := 0; mask < 1<<uint(n); mask++ {
			b := make([]byte, n)
			for i := 0; i < n; i++ {
				if mask&(1<<uint(i)) != 0 {
					b[i] = 'b'
				} else {
					b[i] = 'a'
				}
			}
			checkAgainst(t, build(string(b)), string(b), "ab")
		}
	}
}

// TestIncrementalAppend verifies after every single append.
func TestIncrementalAppend(t *testing.T) {
	s := "abacaba"
	a := New()
	for i := 0; i < len(s); i++ {
		a.Append(s[i])
		checkAgainst(t, a, s[:i+1], "abc")
	}
}

// TestRollbackToOtherSuffix appends one suffix, rolls back, then
// appends a different suffix, verifying at each step.
func TestRollbackToOtherSuffix(t *testing.T) {
	a := New()
	a.AppendString("abab")
	v := a.Version()
	checkAgainst(t, a, "abab", "abc")

	a.AppendString("ab") // "ababab"
	checkAgainst(t, a, "ababab", "abc")

	a.Rollback(v) // back to "abab"
	checkAgainst(t, a, "abab", "abc")

	a.AppendString("ca") // "ababca"
	checkAgainst(t, a, "ababca", "abc")

	// Substrings unique to the discarded suffix must be gone.
	for _, gone := range []string{"ababa", "baba", "babab"} {
		if a.Contains(gone) {
			t.Fatalf("after rollback+rewrite, Contains(%q) = true", gone)
		}
	}
}

// TestRollbackLeavesNoResidualTransitions rolls back and checks that
// strings occurring only in the discarded part are not reachable.
func TestRollbackLeavesNoResidualTransitions(t *testing.T) {
	a := New()
	a.AppendString("aaab")
	a.Rollback(3) // keep "aaa"
	checkAgainst(t, a, "aaa", "ab")
	for _, gone := range []string{"b", "ab", "aab", "aaab"} {
		if a.Contains(gone) {
			t.Fatalf("residual transition: Contains(%q) = true after rollback", gone)
		}
	}
	if got := a.DistinctSubstrings(); got != 3 { // a, aa, aaa
		t.Fatalf("DistinctSubstrings() = %d, want 3", got)
	}
}

// TestPopToEmptyThenRebuild pops every byte and rebuilds differently.
func TestPopToEmptyThenRebuild(t *testing.T) {
	a := New()
	a.AppendString("abacaba")
	for i := len("abacaba"); i > 0; i-- {
		a.Pop()
		checkAgainst(t, a, "abacaba"[:i-1], "abc")
	}
	if a.Len() != 0 || a.DistinctSubstrings() != 0 {
		t.Fatalf("not empty after pops: len=%d distinct=%d", a.Len(), a.DistinctSubstrings())
	}
	a.AppendString("xyxy")
	checkAgainst(t, a, "xyxy", "xy")
}

// TestRollbackCloneHeavy exercises clone creation and rollback on a
// string known to force many clones.
func TestRollbackCloneHeavy(t *testing.T) {
	a := New()
	a.AppendString("ababa")
	v3 := 3 // "aba"
	v4 := 4 // "abab"
	a.AppendString("bab") // "abababab"
	checkAgainst(t, a, "abababab", "ab")
	a.Rollback(v4)
	checkAgainst(t, a, "abab", "ab")
	a.Rollback(v3)
	checkAgainst(t, a, "aba", "ab")
	a.AppendString("caba") // "abacaba"
	checkAgainst(t, a, "abacaba", "abc")
}

// TestEmptyPattern documents and locks in the public convention.
func TestEmptyPattern(t *testing.T) {
	a := build("abc")
	want := []int{0, 1, 2, 3}
	if got := a.Occurrences(""); !equalInts(got, want) {
		t.Fatalf(`Occurrences("") = %v, want %v`, got, want)
	}
	if got := a.Count(""); got != 4 {
		t.Fatalf(`Count("") = %d, want 4`, got)
	}
	if !a.Contains("") {
		t.Fatal(`Contains("") = false, want true`)
	}
	// The empty string is never counted as a distinct substring.
	if got := a.DistinctSubstrings(); got != len(reference("abc")) {
		t.Fatalf("DistinctSubstrings() = %d, want %d", got, len(reference("abc")))
	}
	e := New()
	if got := e.Occurrences(""); !equalInts(got, []int{0}) {
		t.Fatalf(`empty text: Occurrences("") = %v, want [0]`, got)
	}
}

// TestRollbackIdempotent ensures rolling back to the current or a
// future-clamped version is harmless.
func TestRollbackIdempotent(t *testing.T) {
	a := build("ab")
	a.Rollback(a.Version())
	checkAgainst(t, a, "ab", "ab")
	a.Rollback(0)
	checkAgainst(t, a, "", "ab")
	a.Rollback(-1)
	checkAgainst(t, a, "", "ab")
}

func ExampleAutomaton() {
	a := New()
	a.AppendString("banana")
	fmt.Println(a.DistinctSubstrings())
	fmt.Println(a.Occurrences("ana"))
	v := a.Version()
	a.AppendString("na")
	fmt.Println(a.Occurrences("na"))
	a.Rollback(v)
	fmt.Println(a.Occurrences("na"))
	// Output:
	// 15
	// [1 3]
	// [2 4 6]
	// [2 4]
}
