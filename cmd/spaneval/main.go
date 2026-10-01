// Command spaneval replays local telemetry fixtures against a declared
// expected span structure and prints missing/duplicate/unclosed verdicts
// with a public, reproducible score. No monitoring backend is involved.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"spaneval/internal/eval"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "run":
		run(os.Args[2:])
	case "formula":
		fmt.Println(eval.FormulaText)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage:
  spaneval run [-json] <fixture.json>...   replay fixtures and print verdicts
  spaneval formula                         print the public score formula
`)
}

func run(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit reports as JSON")
	fs.Parse(args)
	if fs.NArg() == 0 {
		usage()
		os.Exit(2)
	}
	exit := 0
	for _, path := range fs.Args() {
		f, err := eval.LoadFixture(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			exit = 1
			continue
		}
		reports := eval.Replay(f)
		if *asJSON {
			data, _ := json.MarshalIndent(reports, "", "  ")
			fmt.Println(string(data))
			continue
		}
		printReports(f, reports)
	}
	os.Exit(exit)
}

func printReports(f *eval.Fixture, reports []eval.Report) {
	fmt.Printf("fixture: %s\n", f.Name)
	for _, r := range reports {
		fmt.Printf("  [%s] events=0..%d\n", r.Phase, r.AfterEvent)
		if r.Indeterminate {
			fmt.Println("    status: INDETERMINATE (enumeration budget exhausted, no definite score)")
			continue
		}
		fmt.Println("    status: DETERMINATE")
		fmt.Printf("    score: %.1f/100\n", r.Score)
		fmt.Printf("    slots: required=%d matched=%d missing=%d\n",
			r.Counts.RequiredTotal, r.Counts.Matched, r.Counts.Missing)
		fmt.Printf("    anomalies: duplicates=%d unclosed=%d misplaced=%d unexpected=%d\n",
			r.Counts.Duplicates, r.Counts.Unclosed, r.Counts.Misplaced, r.Counts.Unexpected)
		for _, c := range r.Critical {
			fmt.Printf("    CRITICAL: %s\n", c)
		}
		for _, e := range r.Explanations {
			fmt.Printf("    revision: %s\n", e)
		}
		if len(r.Details) > 0 {
			fmt.Println("    mapping:")
			for _, d := range r.Details {
				fmt.Printf("      %s\n", d)
			}
		}
	}
	fmt.Println()
}
