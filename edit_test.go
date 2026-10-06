package editfa

import (
	"math/rand"
	"testing"
)

// allStrings enumerates every string over alphabet up to length maxLen.
func allStrings(alphabet string, maxLen int) []string {
	out := []string{""}
	cur := []string{""}
	for l := 0; l < maxLen; l++ {
		var next []string
		for _, s := range cur {
			for i := 0; i < len(alphabet); i++ {
				next = append(next, s+string(alphabet[i]))
			}
		}
		out = append(out, next...)
		cur = next
	}
	return out
}

// editDistance is the classic insert/delete/replace distance.
func editDistance(a, b string, c Costs) int {
	prev := make([]int, len(b)+1)
	for j := 1; j <= len(b); j++ {
		prev[j] = prev[j-1] + c.Insert
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = prev[0] + c.Delete
		for j := 1; j <= len(b); j++ {
			best := prev[j] + c.Delete
			if v := cur[j-1] + c.Insert; v < best {
				best = v
			}
			sub := c.Replace
			if a[i-1] == b[j-1] {
				sub = 0
			}
			if v := prev[j-1] + sub; v < best {
				best = v
			}
			cur[j] = best
		}
		prev = cur
	}
	return prev[len(b)]
}

// bruteForceBest enumerates accepted strings up to maxLen and returns
// the best target by (cost, length, lexicographic order).
func bruteForceBest(d *DFA, input string, c Costs, maxLen int) (int, string, bool) {
	bestCost := -1
	bestStr := ""
	type node struct {
		q int
		s string
	}
	queue := []node{{d.Start, ""}}
	alphabet := d.Alphabet()
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if d.acceptsState(cur.q) {
			dc := editDistance(input, cur.s, c)
			if bestCost < 0 || dc < bestCost ||
				(dc == bestCost && (len(cur.s) < len(bestStr) ||
					(len(cur.s) == len(bestStr) && cur.s < bestStr))) {
				bestCost, bestStr = dc, cur.s
			}
		}
		if len(cur.s) < maxLen {
			for _, a := range alphabet {
				if nq, ok := d.step(cur.q, a); ok {
					queue = append(queue, node{nq, cur.s + string(a)})
				}
			}
		}
	}
	return bestCost, bestStr, bestCost >= 0
}

// reachableAccept reports whether any accepting state is reachable.
func reachableAccept(d *DFA) bool {
	seen := make([]bool, d.NumStates())
	queue := []int{d.Start}
	seen[d.Start] = true
	for len(queue) > 0 {
		q := queue[0]
		queue = queue[1:]
		if d.acceptsState(q) {
			return true
		}
		for _, nq := range d.Trans[q] {
			if nq >= 0 && nq < d.NumStates() && !seen[nq] {
				seen[nq] = true
				queue = append(queue, nq)
			}
		}
	}
	return false
}

func testDFAs() map[string]*DFA {
	return map[string]*DFA{
		// Cyclic: single accepting state with self-loops.
		"selfLoopAll": {
			Start:  0,
			Accept: []bool{true},
			Trans:  []map[byte]int{{'a': 0, 'b': 0}},
		},
		// Cyclic: accepts (ab)*.
		"abStar": {
			Start:  0,
			Accept: []bool{true, false},
			Trans:  []map[byte]int{{'a': 1}, {'b': 0}},
		},
		// Cyclic: accepts strings ending in 'b'.
		"endsWithB": {
			Start:  0,
			Accept: []bool{false, true},
			Trans:  []map[byte]int{{'a': 0, 'b': 1}, {'a': 0, 'b': 1}},
		},
		// Accepts exactly "abc".
		"onlyABC": {
			Start:  0,
			Accept: []bool{false, false, false, true},
			Trans: []map[byte]int{
				{'a': 1}, {'b': 2}, {'c': 3}, {},
			},
		},
		// Accepts exactly "ab" or "ac".
		"abOrAc": {
			Start:  0,
			Accept: []bool{false, false, true},
			Trans:  []map[byte]int{{'a': 1}, {'b': 2, 'c': 2}, {}},
		},
		// Accepts only the empty string.
		"emptyOnly": {
			Start:  0,
			Accept: []bool{true, false},
			Trans:  []map[byte]int{{'a': 1, 'b': 1}, {'a': 1, 'b': 1}},
		},
		// No accepting state: no solution exists.
		"noAccept": {
			Start:  0,
			Accept: []bool{false, false},
			Trans:  []map[byte]int{{'a': 1, 'b': 0}, {'a': 1, 'b': 1}},
		},
	}
}

// randomDFA builds a complete random automaton over {a,b}.
func randomDFA(rng *rand.Rand) *DFA {
	ns := 1 + rng.Intn(4)
	d := &DFA{Start: 0, Accept: make([]bool, ns), Trans: make([]map[byte]int, ns)}
	for q := 0; q < ns; q++ {
		d.Accept[q] = rng.Intn(2) == 0
		d.Trans[q] = map[byte]int{'a': rng.Intn(ns), 'b': rng.Intn(ns)}
	}
	return d
}

func checkResult(t *testing.T, name string, d *DFA, input string, c Costs) {
	t.Helper()
	res, err := MinEditScript(d, input, c)
	if !reachableAccept(d) {
		if err != ErrNoSolution {
			t.Errorf("%s input %q costs %+v: expected ErrNoSolution, got %v", name, input, c, err)
		}
		return
	}
	if err != nil {
		t.Errorf("%s input %q costs %+v: unexpected error: %v", name, input, c, err)
		return
	}
	// The script must replay to the reported output.
	got, err := Apply(input, res.Script)
	if err != nil {
		t.Errorf("%s input %q costs %+v: Apply failed: %v (script %v)", name, input, c, err, res.Script)
		return
	}
	if got != res.Output {
		t.Errorf("%s input %q costs %+v: Apply produced %q, result says %q", name, input, c, got, res.Output)
	}
	// The output must be accepted and the cost must match the script.
	if !d.Accepts(res.Output) {
		t.Errorf("%s input %q costs %+v: output %q not accepted", name, input, c, res.Output)
	}
	if sc := ScriptCost(res.Script, c); sc != res.Cost {
		t.Errorf("%s input %q costs %+v: script costs %d, result says %d", name, input, c, sc, res.Cost)
	}
	// Cross-check against brute force. Any accepted string longer than
	// maxLen costs more than res.Cost in inserts alone, so the bound
	// cannot hide a better or equally good candidate.
	maxLen := len(input) + res.Cost/c.Insert
	bc, bo, ok := bruteForceBest(d, input, c, maxLen)
	if !ok {
		t.Errorf("%s input %q costs %+v: brute force found nothing", name, input, c)
		return
	}
	if bc != res.Cost || bo != res.Output {
		t.Errorf("%s input %q costs %+v: got cost=%d output=%q, brute force says cost=%d output=%q",
			name, input, c, res.Cost, res.Output, bc, bo)
	}
}

func TestExhaustiveSmallAutomata(t *testing.T) {
	dfas := testDFAs()
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 15; i++ {
		dfas[string(rune('a'+i))+"-random"] = randomDFA(rng)
	}
	inputs := append(allStrings("ab", 4), "x", "axb", "bxa")
	costsList := []Costs{
		{Insert: 1, Delete: 1, Replace: 1},
		{Insert: 1, Delete: 2, Replace: 3},
		{Insert: 3, Delete: 1, Replace: 2},
	}
	for name, d := range dfas {
		for _, input := range inputs {
			for _, c := range costsList {
				checkResult(t, name, d, input, c)
			}
		}
	}
}

func TestEmptyInput(t *testing.T) {
	// Only "ab" is accepted: the script must be pure inserts.
	d := &DFA{
		Start:  0,
		Accept: []bool{false, false, true},
		Trans:  []map[byte]int{{'a': 1}, {'b': 2}, {}},
	}
	res, err := MinEditScript(d, "", Costs{Insert: 2, Delete: 1, Replace: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "ab" || res.Cost != 4 {
		t.Fatalf("got cost=%d output=%q, want cost=4 output=%q", res.Cost, res.Output, "ab")
	}
	for _, e := range res.Script {
		if e.Op != Insert {
			t.Fatalf("expected all-insert script, got %v", res.Script)
		}
	}
	if got, err := Apply("", res.Script); err != nil || got != "ab" {
		t.Fatalf("Apply = %q, %v", got, err)
	}

	// Start state accepting: empty script, empty output, zero cost.
	d2 := &DFA{
		Start:  0,
		Accept: []bool{true},
		Trans:  []map[byte]int{{'a': 0}},
	}
	res, err = MinEditScript(d2, "", Costs{Insert: 1, Delete: 1, Replace: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "" || res.Cost != 0 || len(res.Script) != 0 {
		t.Fatalf("got %+v, want empty result", res)
	}
}

func TestCyclicAutomaton(t *testing.T) {
	// Accepts (ab)*; input "aba" ties between deleting the last 'a'
	// (output "ab") and inserting a 'b' (output "abab"). Equal cost,
	// so the shorter output wins.
	d := &DFA{
		Start:  0,
		Accept: []bool{true, false},
		Trans:  []map[byte]int{{'a': 1}, {'b': 0}},
	}
	c := Costs{Insert: 1, Delete: 1, Replace: 1}
	res, err := MinEditScript(d, "aba", c)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "ab" || res.Cost != 1 {
		t.Fatalf("got cost=%d output=%q, want cost=1 output=%q", res.Cost, res.Output, "ab")
	}

	// Input "a": deleting yields "" (accepted, length 0), inserting
	// yields "ab" (length 2). Same cost, shortest output wins.
	res, err = MinEditScript(d, "a", c)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "" || res.Cost != 1 {
		t.Fatalf("got cost=%d output=%q, want cost=1 output=%q", res.Cost, res.Output, "")
	}

	// Loop around the cycle several times.
	res, err = MinEditScript(d, "abababx", c)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "ababab" || res.Cost != 1 {
		t.Fatalf("got cost=%d output=%q, want cost=1 output=%q", res.Cost, res.Output, "ababab")
	}
}

func TestTieBreaks(t *testing.T) {
	c := Costs{Insert: 1, Delete: 1, Replace: 1}

	// Shortest output wins over lexicographic order: "ab" (replace)
	// and "c" (delete) both cost 1 for input "ac".
	d := &DFA{
		Start:  0,
		Accept: []bool{false, false, true, true},
		Trans: []map[byte]int{
			{'a': 1, 'c': 3}, {'b': 2}, {}, {},
		},
	}
	res, err := MinEditScript(d, "ac", c)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "c" || res.Cost != 1 {
		t.Fatalf("shortest tie-break: got cost=%d output=%q, want cost=1 output=%q", res.Cost, res.Output, "c")
	}

	// Lexicographic order breaks equal-cost, equal-length ties.
	d2 := &DFA{
		Start:  0,
		Accept: []bool{false, false, true},
		Trans:  []map[byte]int{{'a': 1}, {'b': 2, 'c': 2}, {}},
	}
	res, err = MinEditScript(d2, "ax", c)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "ab" || res.Cost != 1 {
		t.Fatalf("lexicographic tie-break: got cost=%d output=%q, want cost=1 output=%q", res.Cost, res.Output, "ab")
	}
}

func TestNoSolutionAndInvalidInput(t *testing.T) {
	d := &DFA{
		Start:  0,
		Accept: []bool{false},
		Trans:  []map[byte]int{{'a': 0}},
	}
	if _, err := MinEditScript(d, "aaa", Costs{Insert: 1, Delete: 1, Replace: 1}); err != ErrNoSolution {
		t.Fatalf("expected ErrNoSolution, got %v", err)
	}
	good := &DFA{Start: 0, Accept: []bool{true}, Trans: []map[byte]int{{}}}
	if _, err := MinEditScript(good, "", Costs{Insert: 0, Delete: 1, Replace: 1}); err == nil {
		t.Fatal("expected error for non-positive insert cost")
	}
	if _, err := MinEditScript(nil, "", Costs{Insert: 1, Delete: 1, Replace: 1}); err == nil {
		t.Fatal("expected error for nil DFA")
	}
	bad := &DFA{Start: 5, Accept: []bool{true}, Trans: []map[byte]int{{}}}
	if _, err := MinEditScript(bad, "", Costs{Insert: 1, Delete: 1, Replace: 1}); err == nil {
		t.Fatal("expected error for out-of-range start state")
	}
}

func TestApplyErrors(t *testing.T) {
	if _, err := Apply("a", []Edit{{Op: Delete, Pos: 1, Old: 'a'}}); err == nil {
		t.Fatal("expected position mismatch error")
	}
	if _, err := Apply("a", []Edit{{Op: Delete, Pos: 0, Old: 'b'}}); err == nil {
		t.Fatal("expected byte mismatch error")
	}
	if _, err := Apply("a", nil); err == nil {
		t.Fatal("expected unconsumed input error")
	}
	if got, err := Apply("", nil); err != nil || got != "" {
		t.Fatalf("Apply empty = %q, %v", got, err)
	}
	if _, err := Apply("ab", []Edit{{Op: Op(99), Pos: 0}}); err == nil {
		t.Fatal("expected unknown op error")
	}
}
