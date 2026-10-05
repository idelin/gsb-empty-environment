// Command hexreplay replays a JSON scenario file against both the
// partitioned store and the flat reference, prints a JSON report with
// per-query results and access stats, and exits non-zero on any mismatch.
//
// Usage: hexreplay <replay.json>
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"hexgrid"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: hexreplay <replay.json>")
		os.Exit(2)
	}
	f, err := hexgrid.LoadReplayFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	report := hexgrid.RunReplay(f)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if !report.OK {
		os.Exit(1)
	}
}
