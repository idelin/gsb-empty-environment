package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"localfmt"
)

func main() {
	start := flag.Int("start", 0, "selection start byte offset")
	end := flag.Int("end", 0, "selection end byte offset (exclusive)")
	apply := flag.Bool("w", false, "write the patched source to stdout instead of the report")
	flag.Parse()

	src, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	res, err := localfmt.FormatSelection(string(src), *start, *end)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *apply {
		fmt.Print(localfmt.Apply(string(src), res.Patch))
		return
	}
	report := map[string]any{
		"patch": map[string]any{
			"start":       res.Patch.Start,
			"end":         res.Patch.End,
			"replacement": res.Patch.Replacement,
		},
		"nodeKind":   res.NodeKind.String(),
		"nodeSpan":   res.NodeSpan,
		"rationale":  res.Rationale,
		"verified":   res.Verified,
		"verifyLogs": res.VerifyNotes,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(report)
}
