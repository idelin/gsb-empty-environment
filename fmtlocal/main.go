package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	file := flag.String("file", "", "input file (default: stdin)")
	start := flag.Int("start", 0, "selection start, byte offset (inclusive)")
	end := flag.Int("end", -1, "selection end, byte offset (exclusive); -1 means a cursor at -start")
	showDiff := flag.Bool("diff", false, "also print a unified diff of the patch")
	flag.Parse()

	var src []byte
	var err error
	if *file == "" {
		src, err = io.ReadAll(os.Stdin)
	} else {
		src, err = os.ReadFile(*file)
	}
	if err != nil {
		fatal(err)
	}

	selEnd := *end
	if selEnd < 0 {
		selEnd = *start
	}

	res, err := formatSelection(string(src), *start, selEnd)
	if err != nil {
		fatal(err)
	}

	out, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(out))

	if *showDiff {
		fmt.Print(unifiedDiff(string(src), res))
	}

	if !res.Checks.OutsideUnchanged || !res.Checks.Idempotent || !res.Checks.SemanticsPreserved {
		fmt.Fprintln(os.Stderr, "verification failed")
		os.Exit(1)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(2)
}

// unifiedDiff renders the single-hunk patch with one line of context.
func unifiedDiff(src string, res *Result) string {
	p := res.Patch
	lineStart := strings.LastIndex(src[:p.Start], "\n") + 1
	lineEnd := len(src)
	if i := strings.Index(src[p.End:], "\n"); i >= 0 {
		lineEnd = p.End + i
	}
	startLine := 1 + strings.Count(src[:p.Start], "\n")

	var b strings.Builder
	b.WriteString("--- a/input\n+++ b/input\n")
	fmt.Fprintf(&b, "@@ -%d +%d @@\n", startLine, startLine)
	if lineStart < p.Start {
		fmt.Fprintf(&b, " %s\n", src[lineStart:p.Start])
	}
	for _, l := range strings.Split(p.Original, "\n") {
		fmt.Fprintf(&b, "-%s\n", l)
	}
	for _, l := range strings.Split(p.Replacement, "\n") {
		fmt.Fprintf(&b, "+%s\n", l)
	}
	if p.End < lineEnd {
		fmt.Fprintf(&b, " %s\n", src[p.End:lineEnd])
	}
	return b.String()
}
