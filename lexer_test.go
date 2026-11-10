package main

import (
	"bytes"
	"math/rand"
	"testing"
)

func sideAt(mode escapeMode, word []byte, index int) bool {
	state := stOutside
	for i, b := range word {
		if i == index {
			return isInside(state, b)
		}
		state = nextState(mode, state, b)
	}
	return false
}

func TestBackslashOffTreatsBackslashAsContent(t *testing.T) {
	// '\'; with escapes off: backslash is ordinary content, so the following
	// quote closes the string and the semicolon lands outside it.
	word := []byte("'\\';")
	if got, want := sideAt(escOff, word, 3), false; got != want {
		t.Fatalf("esc-off semicolon inside = %v, want %v", got, want)
	}

	// Doubled quote still closes-vs-literal correctly without backslash help.
	doubled := []byte("'';")
	if got := sideAt(escOff, doubled, 2); got {
		t.Fatalf("esc-off semicolon after '' must be outside")
	}
}

func TestBackslashOnEscapesQuote(t *testing.T) {
	word := []byte("'\\';")
	if got, want := sideAt(escOn, word, 3), true; got != want {
		t.Fatalf("esc-on semicolon inside = %v, want %v", got, want)
	}

	doubled := []byte("'';")
	if got := sideAt(escOn, doubled, 2); got {
		t.Fatalf("esc-on semicolon after '' must be outside")
	}

	// An escaped backslash must not also escape the following quote.
	escapedBack := []byte("'\\\\';")
	if got := sideAt(escOn, escapedBack, 4); got {
		t.Fatalf("esc-on semicolon after escaped backslash + quote must be outside")
	}
}

func TestDoubledQuotesEquivalentInBothModes(t *testing.T) {
	for _, word := range [][]byte{[]byte("''"), []byte("a''b"), []byte("'it''s';")} {
		off, on := stOutside, stOutside
		for i, b := range word {
			off = nextState(escOff, off, b)
			on = nextState(escOn, on, b)
			if isInside(off, b) != isInside(on, b) {
				t.Fatalf("word %q diverges at index %d without backslash interaction", word, i)
			}
		}
		if off != on {
			t.Fatalf("word %q ends in different states: off=%s on=%s", word, off, on)
		}
	}
}

func TestShortestDivergenceDefaultDictionary(t *testing.T) {
	dictionary := [][]byte{[]byte("'"), []byte("\\"), []byte(";"), []byte("';")}
	result := shortestDivergence(dictionary, 4)
	if result.best == nil {
		t.Fatal("expected a differential candidate")
	}
	if got, want := string(result.best.word), "'\\';"; got != want {
		t.Fatalf("word = %q, want %q", got, want)
	}
	if result.best.divergenceIndex != 3 {
		t.Fatalf("divergence index = %d, want 3", result.best.divergenceIndex)
	}

	rows := replay(result.best.word)
	diffRow := rows[3]
	if sideAt(escOff, result.best.word, 3) != isInside(diffRow.offPre, ';') {
		t.Fatal("trace disagrees with esc-off classification")
	}
	if isInside(diffRow.offPre, ';') || !isInside(diffRow.onPre, ';') {
		t.Fatalf("want esc-off OUT / esc-on IN, got off=%s on=%s",
			side(diffRow.offPre, ';'), side(diffRow.onPre, ';'))
	}
}

func TestShortestDivergenceTieBreakIsByteOrder(t *testing.T) {
	// Two equally short concatenations exist; only the leading byte differs.
	dictionary := [][]byte{[]byte("b'\\"), []byte("a'\\"), []byte("';")}
	result := shortestDivergence(dictionary, 2)
	if result.best == nil {
		t.Fatal("expected a differential candidate")
	}
	if got, want := string(result.best.word), "a'\\';"; got != want {
		t.Fatalf("word = %q, want lexicographically smallest %q", got, want)
	}
}

func TestShortestDivergenceNoSolution(t *testing.T) {
	dictionary := [][]byte{[]byte("'"), []byte(";")}
	if result := shortestDivergence(dictionary, 3); result.best != nil {
		t.Fatalf("unexpected candidate without backslashes: %q", result.best.word)
	}
}

func TestShortestDivergenceRespectsBudget(t *testing.T) {
	dictionary := [][]byte{[]byte("'\\"), []byte("';")}
	if result := shortestDivergence(dictionary, 1); result.best != nil {
		t.Fatalf("one concatenation cannot diverge, got %q", result.best.word)
	}
	result := shortestDivergence(dictionary, 2)
	if result.best == nil || string(result.best.word) != "'\\';" {
		t.Fatalf("two concatenations should yield '\\';, got %+v", result.best)
	}
}

func TestSettledStatesCappedByProductAutomaton(t *testing.T) {
	dictionary := [][]byte{[]byte("'"), []byte("\\"), []byte(";"), []byte("a")}
	result := shortestDivergence(dictionary, 6)
	if result.best == nil {
		t.Fatal("expected a candidate")
	}
	if result.visited > 4*4*7 {
		t.Fatalf("settled %d states, expected at most 16 states per depth level", result.visited)
	}
}

// bruteForce enumerates every fragment sequence up to maxConcat and keeps the
// shortest differential word (lexicographic tie-break), mirroring the search.
func bruteForce(dictionary [][]byte, maxConcat int) []byte {
	var best []byte

	var consider func(prefix []byte, depth int, off, on lexState)
	consider = func(prefix []byte, depth int, off, on lexState) {
		if depth == maxConcat {
			return
		}
		for _, fragment := range dictionary {
			nextOff, nextOn := off, on
			hit := -1
			for i, b := range fragment {
				if b == ';' && isInside(nextOff, b) != isInside(nextOn, b) {
					hit = i
					break
				}
				nextOff = nextState(escOff, nextOff, b)
				nextOn = nextState(escOn, nextOn, b)
			}
			word := append(append([]byte(nil), prefix...), fragment...)
			if hit >= 0 {
				word = word[:len(prefix)+hit+1]
				if best == nil || len(word) < len(best) ||
					(len(word) == len(best) && bytes.Compare(word, best) < 0) {
					best = append([]byte(nil), word...)
				}
				continue
			}
			consider(word, depth+1, nextOff, nextOn)
		}
	}

	consider(nil, 0, stOutside, stOutside)
	return best
}

func TestSearchMatchesBruteForce(t *testing.T) {
	alphabet := []byte{'\'', '\\', ';', 'a'}
	rng := rand.New(rand.NewSource(42))

	for iteration := 0; iteration < 60; iteration++ {
		fragmentCount := 2 + rng.Intn(3)
		dictionary := make([][]byte, fragmentCount)
		seen := map[string]bool{}
		for i := range dictionary {
			var fragment []byte
			for {
				fragment = make([]byte, 1+rng.Intn(2))
				for j := range fragment {
					fragment[j] = alphabet[rng.Intn(len(alphabet))]
				}
				if !seen[string(fragment)] {
					seen[string(fragment)] = true
					break
				}
			}
			dictionary[i] = fragment
		}

		maxConcat := 2 + rng.Intn(2)
		result := shortestDivergence(dictionary, maxConcat)
		want := bruteForce(dictionary, maxConcat)
		if (result.best == nil) != (want == nil) {
			t.Fatalf("dict=%v n=%d: existence mismatch search=%v brute=%q",
				dictionary, maxConcat, result.best != nil, want)
		}
		if want != nil && !bytes.Equal(result.best.word, want) {
			t.Fatalf("dict=%v n=%d: search=%q brute=%q",
				dictionary, maxConcat, result.best.word, want)
		}
	}
}
