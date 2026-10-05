package dagpath

import (
	"math/rand"
	"strings"
	"testing"
)

// buildRopeRagged concatenates s as randomly sized leaf chunks, exercising
// unbalanced concat trees rather than a single leaf.
func buildRopeRagged(r *rand.Rand, s string) *rope {
	root := emptyRope
	for i := 0; i < len(s); {
		j := i + 1 + r.Intn(max(1, min(5, len(s)-i)))
		if j > len(s) {
			j = len(s)
		}
		root = concatRope(root, leafRope(s[i:j]))
		i = j
	}
	return root
}

func TestRopeComparePrefixRelations(t *testing.T) {
	pairs := [][2]string{
		{"", ""},
		{"", "a"},
		{"a", ""},
		{"a", "aa"},
		{"aa", "a"},
		{"ab", "abc"},
		{"abc", "ab"},
		{"abc", "abd"},
		{"abd", "abc"},
		{"b", "ba"},
		{"ba", "b"},
		{"abc", "abc"},
		{"a", "b"},
		{"z", "aa"}, // first byte decides, not length
	}
	for _, p := range pairs {
		got := compareRope(leafRope(p[0]), leafRope(p[1]))
		want := strings.Compare(p[0], p[1])
		if got != want {
			t.Errorf("compareRope(%q, %q) = %d, want %d", p[0], p[1], got, want)
		}
	}
}

func TestRopeCompareAgainstNaive(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	alphabet := "aab" // skewed to force ties and prefix relations
	for iter := 0; iter < 20000; iter++ {
		x := randomString(r, alphabet, 12)
		y := randomString(r, alphabet, 12)
		rx, ry := buildRopeRagged(r, x), buildRopeRagged(r, y)
		if got := ropeString(rx); got != x {
			t.Fatalf("ropeString = %q, want %q", got, x)
		}
		got := compareRope(rx, ry)
		want := strings.Compare(x, y)
		if got != want {
			t.Fatalf("compareRope(%q, %q) = %d, want %d", x, y, got, want)
		}
	}
}

func TestRopeSharedSuffix(t *testing.T) {
	// Many candidates share one big suffix rope; comparisons must not
	// materialize or rescan it, and results must match naive comparison.
	suffix := leafRope(strings.Repeat("q", 1000))
	r := rand.New(rand.NewSource(2))
	for iter := 0; iter < 2000; iter++ {
		x := randomString(r, "ab", 6)
		y := randomString(r, "ab", 6)
		rx := concatRope(leafRope(x), suffix)
		ry := concatRope(leafRope(y), suffix)
		got := compareRope(rx, ry)
		want := strings.Compare(x+suffix.str, y+suffix.str)
		if got != want {
			t.Fatalf("compareRope(%q+S, %q+S) = %d, want %d", x, y, got, want)
		}
	}
}

func randomString(r *rand.Rand, alphabet string, maxLen int) string {
	n := r.Intn(maxLen + 1)
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[r.Intn(len(alphabet))]
	}
	return string(b)
}
