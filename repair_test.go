package repair

import (
	"math/rand"
	"testing"
)

// editDistance is a plain Wagner-Fischer DP between two strings under the
// given operation costs, used as an independent oracle.
func editDistance(a, b string, c Costs) int {
	n, m := len(a), len(b)
	prev := make([]int, m+1)
	for j := 1; j <= m; j++ {
		prev[j] = prev[j-1] + c.Insert
	}
	for i := 1; i <= n; i++ {
		cur := make([]int, m+1)
		cur[0] = prev[0] + c.Delete
		for j := 1; j <= m; j++ {
			best := prev[j] + c.Delete              // delete a[i-1]
			if v := cur[j-1] + c.Insert; v < best { // insert b[j-1]
				best = v
			}
			if a[i-1] == b[j-1] {
				if prev[j-1] < best {
					best = prev[j-1]
				}
			} else if v := prev[j-1] + c.Substitute; v < best {
				best = v
			}
			cur[j] = best
		}
		prev = cur
	}
	return prev[m]
}

// bruteForce enumerates all strings over the alphabet up to length maxLen
// and returns the best accepted one by (cost, length, lexicographic order).
func bruteForce(t *testing.T, d *DFA, input string, c Costs, maxLen int) (int, string) {
	t.Helper()
	bestCost := -1
	bestOut := ""
	var rec func(s []byte)
	rec = func(s []byte) {
		str := string(s)
		if d.Accepts(str) {
			cost := editDistance(input, str, c)
			if bestCost < 0 || cost < bestCost ||
				(cost == bestCost && (len(str) < len(bestOut) ||
					(len(str) == len(bestOut) && str < bestOut))) {
				bestCost, bestOut = cost, str
			}
		}
		if len(s) == maxLen {
			return
		}
		for _, a := range d.Alphabet {
			rec(append(s, a))
		}
	}
	rec(nil)
	if bestCost < 0 {
		t.Fatalf("brute force found no accepted string up to length %d", maxLen)
	}
	return bestCost, bestOut
}

// maxOutputLen bounds the output length of an optimal repair. In the
// product graph (state x input position) the position never decreases, so
// any cycle stays at one position and consists of insertions only; removing
// it lowers both cost and output length. Hence an optimal path inserts at
// most NumStates-1 bytes at each of the len(input)+1 positions and consumes
// each input byte at most once.
func maxOutputLen(d *DFA, input string) int {
	n := len(input)
	return (n+1)*(d.NumStates-1) + n
}

// checkScript verifies that the returned script applies cleanly, produces
// the reported output and cost, and yields an accepted string.
func checkScript(t *testing.T, d *DFA, input string, c Costs) Result {
	t.Helper()
	res, err := Repair(d, input, c)
	if err != nil {
		t.Fatalf("Repair(%q) failed: %v", input, err)
	}
	out, err := ApplyScript(input, res.Script)
	if err != nil {
		t.Fatalf("script does not apply to %q: %v", input, err)
	}
	if out != res.Output {
		t.Fatalf("script produces %q, result claims %q", out, res.Output)
	}
	if got := ScriptCost(res.Script, c); got != res.Cost {
		t.Fatalf("script cost %d != result cost %d", got, res.Cost)
	}
	if !d.Accepts(res.Output) {
		t.Fatalf("output %q is not accepted by the DFA", res.Output)
	}
	return res
}

// checkAgainstBruteForce additionally compares the result with exhaustive
// enumeration of all short strings.
func checkAgainstBruteForce(t *testing.T, d *DFA, input string, c Costs) {
	t.Helper()
	res := checkScript(t, d, input, c)
	wantCost, wantOut := bruteForce(t, d, input, c, maxOutputLen(d, input))
	if res.Cost != wantCost || res.Output != wantOut {
		t.Fatalf("Repair(%q) = (cost %d, out %q), brute force wants (cost %d, out %q)",
			input, res.Cost, res.Output, wantCost, wantOut)
	}
}

// parityDFA accepts strings with an even number of 'a's; it is cyclic.
func parityDFA() *DFA {
	return &DFA{
		NumStates: 2,
		Alphabet:  []byte{'a', 'b'},
		Delta: [][]int{
			{1, 0}, // state 0: 'a' flips parity, 'b' loops
			{0, 1},
		},
		Start:  0,
		Accept: []bool{true, false},
	}
}

// endsWithBDFA accepts non-empty strings ending in 'b'; cyclic.
func endsWithBDFA() *DFA {
	return &DFA{
		NumStates: 2,
		Alphabet:  []byte{'a', 'b'},
		Delta: [][]int{
			{0, 1},
			{0, 1},
		},
		Start:  0,
		Accept: []bool{false, true},
	}
}

// mod3DFA accepts strings whose number of 'a's is a multiple of 3; cyclic.
func mod3DFA() *DFA {
	return &DFA{
		NumStates: 3,
		Alphabet:  []byte{'a', 'b'},
		Delta: [][]int{
			{1, 0},
			{2, 1},
			{0, 2},
		},
		Start:  0,
		Accept: []bool{true, false, false},
	}
}

func TestEmptyInputEmptyAccepted(t *testing.T) {
	c := Costs{Insert: 1, Delete: 1, Substitute: 1}
	res, err := Repair(parityDFA(), "", c)
	if err != nil {
		t.Fatal(err)
	}
	if res.Cost != 0 || res.Output != "" || len(res.Script) != 0 {
		t.Fatalf("got %+v, want zero-cost empty repair", res)
	}
	checkAgainstBruteForce(t, parityDFA(), "", c)
}

func TestEmptyInputNeedsInsert(t *testing.T) {
	c := Costs{Insert: 2, Delete: 1, Substitute: 3}
	// Accepts only strings ending in 'b'; empty input needs one insert.
	res, err := Repair(endsWithBDFA(), "", c)
	if err != nil {
		t.Fatal(err)
	}
	if res.Cost != 2 || res.Output != "b" {
		t.Fatalf("got %+v, want cost 2 output %q", res, "b")
	}
	checkAgainstBruteForce(t, endsWithBDFA(), "", c)
}

func TestEmptyInputCyclicAutomaton(t *testing.T) {
	// mod3 with start in a non-accepting residue forces cyclic inserts.
	d := mod3DFA()
	d.Start = 1
	c := Costs{Insert: 1, Delete: 1, Substitute: 1}
	res, err := Repair(d, "", c)
	if err != nil {
		t.Fatal(err)
	}
	// Cheapest: insert "aa" (cost 2) to reach residue 0; "aab" etc. cost more.
	if res.Cost != 2 || res.Output != "aa" {
		t.Fatalf("got %+v, want cost 2 output %q", res, "aa")
	}
	checkAgainstBruteForce(t, d, "", c)
}

func TestCyclicAutomatonVariousInputs(t *testing.T) {
	costs := []Costs{
		{Insert: 1, Delete: 1, Substitute: 1},
		{Insert: 2, Delete: 1, Substitute: 3},
		{Insert: 1, Delete: 4, Substitute: 2},
		{Insert: 3, Delete: 2, Substitute: 5},
	}
	// Short inputs get the full brute-force cross-check; longer inputs
	// (deeper into automaton cycles) get the script-application check to
	// keep enumeration tractable.
	short := []string{"", "a", "b", "ab", "ba", "aab", "x", "axb"}
	long := []string{"bbba", "ababab", "baabba", "xxxx"}
	dfas := map[string]*DFA{
		"parity":    parityDFA(),
		"endsWithB": endsWithBDFA(),
		"mod3":      mod3DFA(),
	}
	for name, d := range dfas {
		for _, c := range costs {
			for _, in := range short {
				t.Run(name+"/"+in, func(t *testing.T) {
					checkAgainstBruteForce(t, d, in, c)
				})
			}
			for _, in := range long {
				t.Run(name+"/long/"+in, func(t *testing.T) {
					checkScript(t, d, in, c)
				})
			}
		}
	}
}

func TestTieBreakShortestThenLexicographic(t *testing.T) {
	// Accepts {"a", "b"}: from state 0 both symbols reach the accepting
	// sink 1. Empty input, unit insert: "a" wins on lexicographic order.
	d := &DFA{
		NumStates: 2,
		Alphabet:  []byte{'a', 'b'},
		Delta: [][]int{
			{1, 1},
			{1, 1},
		},
		Start:  0,
		Accept: []bool{false, true},
	}
	c := Costs{Insert: 1, Delete: 1, Substitute: 1}
	res, err := Repair(d, "", c)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "a" || res.Cost != 1 {
		t.Fatalf("got %+v, want cost 1 output %q", res, "a")
	}

	// Same automaton, input "x" (outside the alphabet): substituting by
	// 'a' or 'b' both cost 1 with output length 1; "a" wins.
	res, err = Repair(d, "x", c)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "a" || res.Cost != 1 {
		t.Fatalf("got %+v, want cost 1 output %q", res, "a")
	}
	checkAgainstBruteForce(t, d, "x", c)

	// Shortest output breaks a cost tie: from "a" both "b" (substitute,
	// cost 1, length 1) and "aa" (match + insert, cost 1, length 2) are
	// optimal in cost; "b" must win.
	d2 := &DFA{
		NumStates: 4,
		Alphabet:  []byte{'a', 'b'},
		Delta: [][]int{
			{1, 2},
			{2, 3},
			{3, 3},
			{3, 3},
		},
		Start:  0,
		Accept: []bool{false, false, true, false},
	}
	res, err = Repair(d2, "a", c)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "b" || res.Cost != 1 {
		t.Fatalf("got %+v, want cost 1 output %q", res, "b")
	}
	checkAgainstBruteForce(t, d2, "a", c)
}

func TestSubstituteVersusDeleteInsert(t *testing.T) {
	// Substitution cheaper than delete+insert.
	c := Costs{Insert: 3, Delete: 3, Substitute: 1}
	res, err := Repair(parityDFA(), "a", c)
	if err != nil {
		t.Fatal(err)
	}
	// "a" has odd parity; substitute a->b gives "b" (even) at cost 1.
	if res.Cost != 1 || res.Output != "b" {
		t.Fatalf("got %+v, want cost 1 output %q", res, "b")
	}
	if len(res.Script) != 1 || res.Script[0].Kind != OpSubstitute {
		t.Fatalf("script %v, want a single substitution", res.Script)
	}
}

func TestInvalidInputs(t *testing.T) {
	if _, err := Repair(parityDFA(), "a", Costs{Insert: 0, Delete: 1, Substitute: 1}); err == nil {
		t.Fatal("expected error for zero insert cost")
	}
	if _, err := Repair(parityDFA(), "a", Costs{Insert: 1, Delete: -2, Substitute: 1}); err == nil {
		t.Fatal("expected error for negative delete cost")
	}
	bad := &DFA{NumStates: 1, Alphabet: []byte{'a'}, Delta: [][]int{{0}}, Start: 3, Accept: []bool{true}}
	if _, err := Repair(bad, "", Costs{Insert: 1, Delete: 1, Substitute: 1}); err == nil {
		t.Fatal("expected error for out-of-range start state")
	}
}

func TestApplyScriptErrors(t *testing.T) {
	if _, err := ApplyScript("ab", []Op{{Kind: OpMatch, In: 'x', Out: 'x'}}); err == nil {
		t.Fatal("expected mismatch error")
	}
	if _, err := ApplyScript("ab", []Op{{Kind: OpMatch, In: 'a', Out: 'a'}}); err == nil {
		t.Fatal("expected unconsumed-input error")
	}
}

// TestRandomAutomata cross-checks Repair against brute-force enumeration on
// random small (cyclic) automata, random inputs and random positive costs.
func TestRandomAutomata(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	alphabet := []byte{'a', 'b'}
	for trial := 0; trial < 60; trial++ {
		states := 1 + rng.Intn(3)
		d := &DFA{
			NumStates: states,
			Alphabet:  alphabet,
			Delta:     make([][]int, states),
			Start:     rng.Intn(states),
			Accept:    make([]bool, states),
		}
		for q := 0; q < states; q++ {
			d.Delta[q] = []int{rng.Intn(states), rng.Intn(states)}
		}
		// Choose accepting states among those reachable from the start so
		// the language is never empty.
		reach := make([]bool, states)
		reach[d.Start] = true
		changed := true
		for changed {
			changed = false
			for q := 0; q < states; q++ {
				if !reach[q] {
					continue
				}
				for _, nxt := range d.Delta[q] {
					if !reach[nxt] {
						reach[nxt] = true
						changed = true
					}
				}
			}
		}
		var reachable []int
		for q := 0; q < states; q++ {
			if reach[q] {
				reachable = append(reachable, q)
			}
		}
		for _, q := range reachable {
			d.Accept[q] = rng.Intn(2) == 0
		}
		if !d.Accept[d.Start] {
			anyAccept := false
			for _, q := range reachable {
				anyAccept = anyAccept || d.Accept[q]
			}
			if !anyAccept {
				d.Accept[reachable[rng.Intn(len(reachable))]] = true
			}
		}
		c := Costs{
			Insert:     1 + rng.Intn(4),
			Delete:     1 + rng.Intn(4),
			Substitute: 1 + rng.Intn(4),
		}
		for inTrial := 0; inTrial < 4; inTrial++ {
			l := rng.Intn(4)
			in := make([]byte, l)
			for i := range in {
				if rng.Intn(4) == 0 {
					in[i] = 'x' // occasionally outside the alphabet
				} else {
					in[i] = alphabet[rng.Intn(len(alphabet))]
				}
			}
			checkAgainstBruteForce(t, d, string(in), c)
		}
	}
}
