package contentmatch

import (
	"bytes"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// bruteForce is the reference matcher: for every rule it finds all pattern
// occurrences in the complete payload and backtracks over them, collecting
// every combination that satisfies the constraints.
func bruteForce(payload []byte, rules []Rule) map[int][][]Span {
	res := make(map[int][][]Span)
	for ri, r := range rules {
		occ := make([][]Span, len(r.Patterns))
		for i, p := range r.Patterns {
			for j := 0; j+len(p.Content) <= len(payload); j++ {
				if bytes.Equal(payload[j:j+len(p.Content)], p.Content) {
					occ[i] = append(occ[i], Span{j, j + len(p.Content)})
				}
			}
		}
		var combos [][]Span
		var cur []Span
		var rec func(i, prevEnd int)
		rec = func(i, prevEnd int) {
			if i == len(r.Patterns) {
				combos = append(combos, append([]Span(nil), cur...))
				return
			}
			p := &r.Patterns[i]
			for _, sp := range occ[i] {
				if !absOK(p, sp.Start, sp.End) {
					continue
				}
				if i > 0 && !relOK(p, sp.Start, sp.End, prevEnd) {
					continue
				}
				cur = append(cur, sp)
				rec(i+1, sp.End)
				cur = cur[:len(cur)-1]
			}
		}
		rec(0, -1)
		if len(combos) > 0 {
			res[ri] = combos
		}
	}
	return res
}

func key(spans []Span) string {
	return fmt.Sprint(spans)
}

func normalize(matches []Match) map[int]map[string]bool {
	res := make(map[int]map[string]bool)
	for _, m := range matches {
		if res[m.Rule] == nil {
			res[m.Rule] = make(map[string]bool)
		}
		res[m.Rule][key(m.Spans)] = true
	}
	return res
}

func normalizeBrute(combos map[int][][]Span) map[int]map[string]bool {
	res := make(map[int]map[string]bool)
	for ri, cs := range combos {
		res[ri] = make(map[string]bool)
		for _, c := range cs {
			res[ri][key(c)] = true
		}
	}
	return res
}

func equalMatchSets(a, b map[int]map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for ri, as := range a {
		bs, ok := b[ri]
		if !ok || len(as) != len(bs) {
			return false
		}
		for k := range as {
			if !bs[k] {
				return false
			}
		}
	}
	return true
}

// feedAll feeds payload through a fresh stream, split at the given
// positions, and returns all matches.
func feedAll(t *testing.T, eng *Engine, payload []byte, splits ...int) []Match {
	t.Helper()
	st := eng.NewStream()
	var out []Match
	prev := 0
	for _, sp := range splits {
		if sp < prev || sp > len(payload) {
			t.Fatalf("bad split %d", sp)
		}
		out = append(out, st.Feed(payload[prev:sp])...)
		prev = sp
	}
	out = append(out, st.Feed(payload[prev:])...)
	return out
}

func checkAgainstBruteForce(t *testing.T, eng *Engine, rules []Rule, payload []byte, splits ...int) {
	t.Helper()
	want := normalizeBrute(bruteForce(payload, rules))
	got := normalize(feedAll(t, eng, payload, splits...))
	if !equalMatchSets(got, want) {
		t.Fatalf("payload %q splits %v:\n got %v\nwant %v", payload, splits, got, want)
	}
}

func randomRules(rng *rand.Rand, n int) []Rule {
	rules := make([]Rule, n)
	for i := range rules {
		np := 1 + rng.Intn(3)
		rules[i].Patterns = make([]Pattern, np)
		for j := range rules[i].Patterns {
			l := 1 + rng.Intn(3)
			c := make([]byte, l)
			for k := range c {
				c[k] = "ab"[rng.Intn(2)]
			}
			p := Pattern{Content: c}
			if rng.Intn(2) == 0 {
				p.Offset = rng.Intn(5)
			}
			if rng.Intn(2) == 0 {
				p.Depth = rng.Intn(8)
			}
			if j > 0 {
				if rng.Intn(2) == 0 {
					p.Distance = rng.Intn(4)
				}
				if rng.Intn(2) == 0 {
					p.Within = rng.Intn(8)
				}
			}
			rules[i].Patterns[j] = p
		}
	}
	return rules
}

func randomPayload(rng *rand.Rand, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		if rng.Intn(10) == 0 {
			b[i] = 'c'
		} else {
			b[i] = "ab"[rng.Intn(2)]
		}
	}
	return b
}

// TestStreamingVsBruteForce compares the streaming engine against the
// brute-force reference on random rules and payloads, feeding the payload
// whole, byte by byte, and split at every single position.
func TestStreamingVsBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 200; trial++ {
		rules := randomRules(rng, 1+rng.Intn(4))
		payload := randomPayload(rng, 8+rng.Intn(30))
		eng, err := NewEngine(rules, WithMaxCandidatesPerRule(1<<16))
		if err != nil {
			t.Fatal(err)
		}
		checkAgainstBruteForce(t, eng, rules, payload) // whole payload
		bytewise := make([]int, 1, len(payload))
		for i := 1; i < len(payload); i++ {
			checkAgainstBruteForce(t, eng, rules, payload, i) // single split
			bytewise = append(bytewise, i)
		}
		checkAgainstBruteForce(t, eng, rules, payload, bytewise...) // byte by byte
	}
}

// TestAllSegmentations enumerates every possible segmentation of a payload
// and checks each against the brute-force reference.
func TestAllSegmentations(t *testing.T) {
	rules := []Rule{
		{Patterns: []Pattern{
			{Content: []byte("ab")},
			{Content: []byte("ba"), Distance: 1, Within: 5},
		}},
		{Patterns: []Pattern{
			{Content: []byte("a"), Offset: 1, Depth: 6},
			{Content: []byte("bb"), Within: 4},
			{Content: []byte("ab"), Distance: 0, Within: 6},
		}},
		{Patterns: []Pattern{{Content: []byte("bab")}}},
	}
	eng, err := NewEngine(rules)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("abbababbaabb")
	want := normalizeBrute(bruteForce(payload, rules))
	// Every subset of the len(payload)-1 cut positions is one segmentation.
	for mask := 0; mask < 1<<(len(payload)-1); mask++ {
		var splits []int
		for i := 1; i < len(payload); i++ {
			if mask&(1<<(i-1)) != 0 {
				splits = append(splits, i)
			}
		}
		got := normalize(feedAll(t, eng, payload, splits...))
		if !equalMatchSets(got, want) {
			t.Fatalf("splits %v:\n got %v\nwant %v", splits, got, want)
		}
	}
}

// TestCrossSegmentPattern checks that a pattern spanning a segment boundary
// is found at every split position.
func TestCrossSegmentPattern(t *testing.T) {
	rules := []Rule{{Patterns: []Pattern{
		{Content: []byte("needle")},
		{Content: []byte("hay"), Distance: 0, Within: 4},
	}}}
	eng, err := NewEngine(rules)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("haystack with a needlehay inside")
	want := normalizeBrute(bruteForce(payload, rules))
	if len(want) == 0 {
		t.Fatal("test payload should produce matches")
	}
	for i := 0; i <= len(payload); i++ {
		got := normalize(feedAll(t, eng, payload, i))
		if !equalMatchSets(got, want) {
			t.Fatalf("split at %d: got %v want %v", i, got, want)
		}
	}
}

// TestBacktracking crafts a payload where the first occurrence of a pattern
// cannot complete the rule but a later one can; the engine must keep both
// candidates and backtrack.
func TestBacktracking(t *testing.T) {
	rules := []Rule{{Patterns: []Pattern{
		{Content: []byte("a")},
		{Content: []byte("b"), Distance: 2, Within: 4},
	}}}
	eng, err := NewEngine(rules)
	if err != nil {
		t.Fatal(err)
	}
	// b@3 satisfies distance 2 only relative to a@0, not a@1 or a@2.
	got := feedAll(t, eng, []byte("aaab"), 1, 2, 3)
	if len(got) != 1 || got[0].Spans[0] != (Span{0, 1}) || got[0].Spans[1] != (Span{3, 4}) {
		t.Fatalf("unexpected matches: %v", got)
	}
	// No occurrence of b is reachable from any a here.
	if got := feedAll(t, eng, []byte("aab"), 2); len(got) != 0 {
		t.Fatalf("unexpected matches: %v", got)
	}
}

// TestMemoryBound verifies the per-rule candidate cap is enforced and
// evictions are counted, while within-constrained candidates are pruned
// eagerly even without a cap.
func TestMemoryBound(t *testing.T) {
	rules := []Rule{{Patterns: []Pattern{
		{Content: []byte("a")},
		{Content: []byte("z")}, // never occurs: candidates pile up
	}}}
	eng, err := NewEngine(rules, WithMaxCandidatesPerRule(4))
	if err != nil {
		t.Fatal(err)
	}
	st := eng.NewStream()
	st.Feed(bytes.Repeat([]byte("a"), 1000))
	if st.Candidates() > 4 {
		t.Fatalf("candidate cap violated: %d", st.Candidates())
	}
	if st.Dropped() == 0 {
		t.Fatal("expected evictions to be counted")
	}

	// With a within window, candidates expire without any cap.
	rules[0].Patterns[1].Within = 3
	eng2, err := NewEngine(rules, WithMaxCandidatesPerRule(1<<16))
	if err != nil {
		t.Fatal(err)
	}
	st2 := eng2.NewStream()
	st2.Feed(bytes.Repeat([]byte("a"), 1000))
	if st2.Candidates() > 3 {
		t.Fatalf("within-pruning failed: %d candidates left", st2.Candidates())
	}
}

// TestDeterministicPositions sanity-checks absolute offsets reported by the
// streaming engine against sorted brute-force spans.
func TestDeterministicPositions(t *testing.T) {
	rules := []Rule{
		{Patterns: []Pattern{{Content: []byte("ab"), Offset: 2, Depth: 5}}},
		{Patterns: []Pattern{
			{Content: []byte("ab")},
			{Content: []byte("ab"), Distance: 2},
		}},
	}
	payload := []byte("xxababab")
	eng, err := NewEngine(rules)
	if err != nil {
		t.Fatal(err)
	}
	got := feedAll(t, eng, payload, 3, 5)
	var gotKeys []string
	for _, m := range got {
		gotKeys = append(gotKeys, fmt.Sprintf("%d:%v", m.Rule, m.Spans))
	}
	sort.Strings(gotKeys)
	want := []string{"0:[{2 4}]", "0:[{4 6}]", "1:[{2 4} {6 8}]"}
	if fmt.Sprint(gotKeys) != fmt.Sprint(want) {
		t.Fatalf("got %v want %v", gotKeys, want)
	}
}
