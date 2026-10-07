// Command prefixdiff diffs two prefix-rule permission sets (old vs new) over a
// finite alphabet, where paths are the strings of length exactly maxdepth.
// Deeper prefixes win; at equal depth deny wins. It emits the minimal
// disjoint prefix sets for permission expansion and shrinkage, then verifies
// them by enumerating every path.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	maxEnum := flag.Int64("maxenum", 20_000_000, "refuse path enumeration above this many paths (safety guard)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: prefixdiff [flags] [rule-file]  (stdin when omitted)\n\n")
		fmt.Fprintf(os.Stderr, "rule file format:\n")
		fmt.Fprintf(os.Stderr, "  alphabet abc                 # each rune is one symbol\n")
		fmt.Fprintf(os.Stderr, "  maxdepth 4                   # paths have exactly this length\n")
		fmt.Fprintf(os.Stderr, "  default allow|deny           # optional, default deny\n")
		fmt.Fprintf(os.Stderr, "  old|new allow|deny <prefix>  # '-' = empty (root) prefix\n")
		fmt.Fprintf(os.Stderr, "\nflags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	var in io.Reader = os.Stdin
	name := "<stdin>"
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		in, name = f, flag.Arg(0)
	} else if flag.NArg() > 1 {
		flag.Usage()
		os.Exit(2)
	}

	cfg, err := parseConfig(in)
	if err != nil {
		fatal(fmt.Errorf("%s: %w", name, err))
	}

	if n := PathCount(len(cfg.Alphabet), cfg.MaxDepth); n < 0 || n > *maxEnum {
		fatal(fmt.Errorf("path space too large to enumerate (%s paths); raise -maxenum to force", countOrHuge(n)))
	}

	expanded, shrunk := cfg.Diff()

	fmt.Printf("expanded (old=deny -> new=allow): %d prefix(es)\n", len(expanded))
	for _, p := range expanded {
		fmt.Printf("  %s\n", displayPrefix(p))
	}
	fmt.Printf("shrunk (old=allow -> new=deny): %d prefix(es)\n", len(shrunk))
	for _, p := range shrunk {
		fmt.Printf("  %s\n", displayPrefix(p))
	}

	problems := cfg.Verify(expanded, shrunk)
	total := PathCount(len(cfg.Alphabet), cfg.MaxDepth)
	if len(problems) == 0 {
		fmt.Printf("verify: enumerated %d paths; coverage exact, prefixes disjoint, none redundant: OK\n", total)
		return
	}
	fmt.Printf("verify: FAILED over %d enumerated paths:\n", total)
	for _, p := range problems {
		fmt.Printf("  %s\n", p)
	}
	os.Exit(1)
}

func countOrHuge(n int64) string {
	if n < 0 {
		return "overflowing"
	}
	return fmt.Sprintf("%d", n)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "prefixdiff:", err)
	os.Exit(2)
}
