// testselector picks tests impacted by a change, given old/new call graphs.
//
// Usage:
//   testselector -old old.json -new new.json [-changed pkg.Foo,pkg.Bar] [-format text|json]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"example.com/testselector/selector"
)

func main() {
	oldPath := flag.String("old", "", "old call graph JSON (empty = no old graph)")
	newPath := flag.String("new", "", "new call graph JSON")
	changed := flag.String("changed", "", "comma-separated explicitly changed symbols")
	format := flag.String("format", "text", "output format: text|json")
	flag.Parse()

	oldG, err := selector.LoadGraph(*oldPath)
	if err != nil {
		fatal(err)
	}
	newG, err := selector.LoadGraph(*newPath)
	if err != nil {
		fatal(err)
	}

	var explicit []string
	if *changed != "" {
		explicit = strings.Split(*changed, ",")
	}

	changes := selector.ChangedSymbols(oldG, newG, explicit)
	rep := selector.Select(oldG, newG, changes)

	switch *format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fatal(err)
		}
	default:
		printText(rep)
	}
	if len(rep.Selected) == 0 {
		os.Exit(1)
	}
}

func printText(rep *selector.Report) {
	fmt.Printf("changed symbols (%d):\n", len(rep.Changes))
	for _, c := range rep.Changes {
		fmt.Printf("  %-30s %s\n", c.Symbol, c.Reason)
	}
	fmt.Printf("selected tests (%d):\n", len(rep.Selected))
	for _, s := range rep.Selected {
		fmt.Printf("  %s  (distance %d, via %s)\n", s.Test, s.Distance, s.Root)
		fmt.Printf("    path: %s\n", strings.Join(s.Path, " -> "))
	}
	if len(rep.Uncovered) > 0 {
		fmt.Printf("changed symbols with no path to any test (%d):\n", len(rep.Uncovered))
		for _, u := range rep.Uncovered {
			fmt.Printf("  %s\n", u)
		}
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(2)
}
