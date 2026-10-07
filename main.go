// Command vpacheck finds the shortest word distinguishing two visibly
// pushdown automata (protocol parsers) within bounded word length and
// stack depth, prints both runs step by step, and independently
// verifies minimality by exhaustive word-by-word simulation.
package main

import (
	"fmt"
	"strings"

	"vpacheck/vpa"
)

const (
	maxLen   = 20 // word length bound
	maxDepth = 8  // stack depth bound; deeper is out of range, not rejected
)

func main() {
	a, b := vpa.ProtocolA(), vpa.ProtocolB()

	fmt.Println("=== Automata ===")
	fmt.Println("A: states q0..q3, finals {q0,q1,q3}; correct parser")
	fmt.Println("B: identical, except call c0 in state q3 (inside a c1 element) pushes tag 1 instead of 0")
	fmt.Println("both: dedicated empty-stack return rule -> error state q2; mismatched close -> q2")
	fmt.Printf("bounds: word length <= %d, stack depth <= %d (deeper = out of range, not rejection)\n", maxLen, maxDepth)

	res, err := vpa.FindDistinguishing(a, b, maxLen, maxDepth)
	must(err)

	fmt.Println("\n=== Bounded difference search (product-automaton BFS) ===")
	fmt.Printf("explored %d product configurations, pruned %d out-of-range successors (depth > %d)\n",
		res.Explored, res.PrunedDeep, maxDepth)
	if !res.Found {
		fmt.Printf("no distinguishing word within length <= %d and depth <= %d\n", maxLen, maxDepth)
		fmt.Println("note: this is bounded equivalence only; out-of-range words were excluded, not proven equivalent")
		return
	}
	fmt.Printf("shortest distinguishing word (length %d): %s\n", len(res.Word), a.WordString(res.Word))

	fmt.Println("\n=== Step-by-step simulation (stack states) ===")
	printTraces(a, b, res)

	fmt.Println("\n=== Difference reason ===")
	fmt.Println(res.Reason)

	fmt.Println("\n=== Independent verification ===")
	verify(a, b, res)
}

func printTraces(a, b *vpa.Automaton, res *vpa.DiffResult) {
	fmt.Printf("%-4s %-5s | %-22s | %-22s | %s\n", "step", "sym", "A: state, stack", "B: state, stack", "note (A)")
	fmt.Printf("%-4s %-5s | q%-3d %-16s | q%-3d %-16s |\n", "", "", a.Initial, a.FormatStack(nil), b.Initial, b.FormatStack(nil))
	for i := range res.Trace1 {
		s1, s2 := res.Trace1[i], res.Trace2[i]
		mark := ""
		if s1.StateAfter != s2.StateAfter || a.FormatStack(s1.StackAfter) != b.FormatStack(s2.StackAfter) {
			mark = "   <-- configurations diverge"
		}
		fmt.Printf("%-4d %-5s | q%-3d %-16s | q%-3d %-16s | %s%s\n",
			i+1, a.SymName(s1.Sym),
			s1.StateAfter, a.FormatStack(s1.StackAfter),
			s2.StateAfter, b.FormatStack(s2.StackAfter),
			s1.Note, mark)
	}
	fmt.Printf("verdict: A accepts=%v (q%d), B accepts=%v (q%d)\n", res.Accept1, res.Trace1[len(res.Trace1)-1].StateAfter, res.Accept2, res.Trace2[len(res.Trace2)-1].StateAfter)
}

func verify(a, b *vpa.Automaton, res *vpa.DiffResult) {
	// 1. Word-by-word re-simulation with the independent simulator.
	fa, oka := vpa.Simulate(a, res.Word, maxDepth)
	fb, okb := vpa.Simulate(b, res.Word, maxDepth)
	fmt.Printf("1. independent re-simulation of %q: A accepts=%v (in range %v), B accepts=%v (in range %v)",
		a.WordString(res.Word), fa, oka, fb, okb)
	if fa == res.Accept1 && fb == res.Accept2 && fa != fb {
		fmt.Println(" -- matches the search verdict")
	} else {
		fmt.Println(" -- MISMATCH!")
	}

	// 2. Exhaustive check: no shorter in-range word distinguishes.
	_, _, _, foundShorter, checked := vpa.BruteForce(a, b, len(res.Word)-1, maxDepth)
	fmt.Printf("2. exhaustive simulation of all %d in-range words of length < %d: ", checked, len(res.Word))
	if foundShorter {
		fmt.Println("found a shorter distinguishing word -- NOT minimal!")
	} else {
		fmt.Println("none distinguishes -- minimality confirmed")
	}

	// 3. Independent brute-force search up to the minimal length.
	w, wa, wb, found, checked := vpa.BruteForce(a, b, len(res.Word), maxDepth)
	if found && len(w) == len(res.Word) {
		fmt.Printf("3. brute force over %d in-range words of length <= %d independently finds %q (A=%v, B=%v), same minimal length\n",
			checked, len(res.Word), a.WordString(w), wa, wb)
	} else {
		fmt.Printf("3. brute force disagrees: found=%v word=%v -- INVESTIGATE\n", found, w)
	}

	// 4. Show that exceeding the depth bound is out of range, not rejection.
	deep := make([]int, maxDepth+1) // c0 repeated: depth 9 > 8
	r := a.Run(deep, maxDepth)
	fmt.Printf("4. depth-bound demo: %s on A stops at step %d with stack %s: out of range=%v (not a rejection)\n",
		"c0 x"+fmt.Sprint(maxDepth+1), r.OutOfRangeAt+1, a.FormatStack(r.FinalStack), r.OutOfRange)

	fmt.Println(strings.Repeat("-", 60))
	fmt.Println("Scope note: equivalence is claimed only within length <= 20 and depth <= 8;")
	fmt.Println("out-of-range words are excluded from the claim, never counted as equivalent.")
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
