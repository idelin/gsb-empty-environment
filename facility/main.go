package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	inPath := flag.String("i", "", "input JSON file (default: stdin)")
	outPath := flag.String("o", "", "output JSON file (default: stdout)")
	budget := flag.Int("budget", 0, "time budget in ms for the heuristic fallback")
	forceHeur := flag.Bool("force-heuristic", false, "skip the exact DP and use the heuristic")
	maxExact := flag.Int("max-exact-entries", 0, "cap on n*n*(K+1) states for the exact DP")
	flag.Parse()

	var r io.Reader = os.Stdin
	if *inPath != "" {
		f, err := os.Open(*inPath)
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		r = f
	}
	data, err := io.ReadAll(r)
	if err != nil {
		fatal(err)
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		fatal(fmt.Errorf("invalid input JSON: %w", err))
	}

	out, err := solve(&in, options{
		budgetMs:        *budget,
		forceHeuristic:  *forceHeur,
		maxExactEntries: *maxExact,
	})
	if err != nil {
		fatal(err)
	}

	enc, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fatal(err)
	}
	enc = append(enc, '\n')
	if *outPath != "" {
		if err := os.WriteFile(*outPath, enc, 0o644); err != nil {
			fatal(err)
		}
	} else {
		os.Stdout.Write(enc)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
