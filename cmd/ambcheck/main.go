// Command ambcheck finds the shortest string on which a context-free
// grammar is ambiguous, and prints two distinct parse trees as proof.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"ambiguity"
)

func main() {
	gfile := flag.String("g", "", "grammar file (default: read from stdin)")
	maxLen := flag.Int("n", 8, "maximum string length to test")
	budget := flag.Int("budget", -1, "maximum number of expanded states (negative: unlimited)")
	flag.Parse()

	var r io.Reader = os.Stdin
	if *gfile != "" {
		f, err := os.Open(*gfile)
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		r = f
	}
	g, err := ambiguity.ParseGrammar(r)
	if err != nil {
		fatal(err)
	}

	res := ambiguity.FindAmbiguous(g, *maxLen, *budget)
	switch {
	case res.Exhausted:
		fmt.Printf("inconclusive: state budget exhausted after %d states\n", res.States)
		os.Exit(2)
	case !res.Found:
		fmt.Printf("no ambiguous string of length <= %d (%d states expanded)\n", *maxLen, res.States)
		return
	}

	fmt.Printf("ambiguous string: %s (length %d, %d states expanded)\n",
		ambiguity.JoinSymbols(res.String), len(res.String), res.States)
	for i, t := range res.Trees {
		fmt.Printf("tree %d: %s\n", i+1, t)
	}

	// Independent verification of the returned witnesses.
	for i, t := range res.Trees {
		if err := ambiguity.ValidateTree(g, t); err != nil {
			fatal(fmt.Errorf("tree %d failed validation: %w", i+1, err))
		}
		if got := ambiguity.JoinSymbols(t.Yield()); got != ambiguity.JoinSymbols(res.String) {
			fatal(fmt.Errorf("tree %d yields %q, not the reported string", i+1, got))
		}
	}
	if res.Trees[0].String() == res.Trees[1].String() {
		fatal(fmt.Errorf("the two returned trees are identical"))
	}
	fmt.Println("verified: two distinct valid parse trees for the same string")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "ambcheck:", err)
	os.Exit(1)
}
