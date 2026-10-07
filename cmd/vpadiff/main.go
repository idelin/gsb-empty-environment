// Command vpadiff demonstrates the vpa library: it recognises sample words,
// finds the shortest distinguishing word between two protocol-parser VPAs
// (length <= 20, stack depth <= 8), prints both traces step by step, and
// independently verifies the result by exhaustive word-by-word simulation.
package main

import (
	"fmt"
	"strings"

	"vpakit/vpa"
)

const (
	maxLen   = 20
	maxDepth = 8
)

func main() {
	a, b := vpa.ExamplePair()
	fmt.Printf("Automata: %s (any execs per request) vs %s (<=1 exec per request)\n", a.Name, b.Name)
	fmt.Printf("Alphabet: open=call, close=return (empty-stack rule), exec=internal\n")

	demoRecognition(a)

	fmt.Printf("\n=== Shortest distinguishing word (length <= %d, stack depth <= %d) ===\n", maxLen, maxDepth)
	res, err := vpa.ShortestDistinguishingWord(a, b, maxLen, maxDepth)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	if !res.Found {
		fmt.Println(res.Summary())
		return
	}
	fmt.Printf("FOUND length %d: %s\n", len(res.Word), strings.Join(res.Word, " "))
	fmt.Printf("Explored %d product configurations; %d successor words pruned by the depth bound.\n\n",
		res.Explored, res.PrunedDeep)
	printTrace(res.Word, res.RunA, res.RunB, a.Name, b.Name)
	fmt.Printf("\nDifference: %s.\n", res.Reason)

	fmt.Printf("\n=== Independent verification (word-by-word, separate simulator) ===\n")
	ok, msg := vpa.ConfirmMinimal(a, b, res.Word, maxDepth)
	fmt.Printf("- [%s] %s\n", check(ok), msg)

	rep := vpa.Exhaustive(a, b, 12, maxDepth)
	match := rep.FirstDiffLen == len(res.Word)
	fmt.Printf("- [%s] exhaustive enumeration to length %d: first difference at length %d, BFS says %d\n",
		check(match), rep.MaxLenChecked, rep.FirstDiffLen, len(res.Word))
	fmt.Printf("  simulated %d in-range words one by one; %d words pruned by the depth bound (excluded, not counted as equal)\n",
		rep.InRangeWords, rep.OutOfRangeWords)
	fmt.Printf("  distinguishing words per length: %v\n", rep.DiffCountByLen)

	fmt.Printf("\nNote: depth %d is a range bound, not rejection. Deep words were excluded,\n", maxDepth)
	fmt.Printf("so a negative search result would NOT prove the automata equivalent.\n")
}

func demoRecognition(a *vpa.VPA) {
	fmt.Printf("\n=== Recognition samples on %s (depth limit %d) ===\n", a.Name, maxDepth)
	samples := [][]string{
		{"close"},                         // stray close: empty-stack rule
		{"open", "exec", "exec", "close"}, // nested-free happy path
		{"open", "open", "close", "open"}, // ends inside a request: reject
		repeat("open", maxDepth+1),        // needs depth 9: out of range
	}
	for _, w := range samples {
		r := a.Run(w, maxDepth)
		fmt.Printf("- %-44s -> %s", strings.Join(w, " "), r.Outcome)
		if r.Outcome == vpa.OutOfRange {
			fmt.Printf(" (stopped at symbol %d: %s)", r.ExceededAt, r.Steps[len(r.Steps)-1].Note)
		}
		fmt.Println()
	}
}

func printTrace(word []string, ra, rb vpa.Run, nameA, nameB string) {
	fmt.Printf("Step-by-step (stacks bottom->top):\n")
	fmt.Printf(" %-3s %-5s %-8s %-34s %-34s\n", "pos", "sym", "kind", "A: "+nameA, "B: "+nameB)
	for pos := 1; pos <= len(word); pos++ {
		fmt.Printf(" %-3d %-5s %-8s %-34s %-34s\n",
			pos, word[pos-1], kindOf(ra, rb, pos),
			formatSide(ra, pos), formatSide(rb, pos))
	}
}

func kindOf(ra, rb vpa.Run, pos int) string {
	if pos <= len(ra.Steps) {
		return ra.Steps[pos-1].Kind.String()
	}
	if pos <= len(rb.Steps) {
		return rb.Steps[pos-1].Kind.String()
	}
	return "?"
}

func formatSide(r vpa.Run, pos int) string {
	if pos > len(r.Steps) {
		return "(already in trap)"
	}
	s := r.Steps[pos-1]
	cell := fmt.Sprintf("%s->%s %s", stateName(s.From), stateName(s.To), vpa.FormatStack(s.StackAfter))
	if s.Note != "" {
		cell += " " + s.Note
	}
	return cell
}

func stateName(q int) string {
	if q == -1 {
		return "trap"
	}
	return fmt.Sprintf("%d", q)
}

func repeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func check(ok bool) string {
	if ok {
		return "OK"
	}
	return "FAIL"
}
