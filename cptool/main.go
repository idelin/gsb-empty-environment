// Command cptool analyzes critical paths under interval-uncertain clocks.
//
// Usage:
//
//	cptool [-json] <input.json>
//
// The input describes task lifecycle events (ready/start/end), dependency
// completion edges, and a project origin, each with a known clock-error
// interval. The tool reports the feasible range of the total critical-path
// duration, one realizable shortest and longest explanation, an attribution
// of compute-slow vs. queue-slow, and locates contradictory time
// constraints instead of emitting negative durations.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
)

func main() {
	jsonOut := flag.Bool("json", false, "emit a machine-readable JSON report")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: cptool [-json] <input.json>")
		os.Exit(2)
	}
	data, err := ioutil.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		fmt.Fprintln(os.Stderr, "error: invalid input JSON:", err)
		os.Exit(2)
	}
	rep := Analyze(&in)
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
	} else {
		printReport(os.Stdout, rep)
	}
	if !rep.Feasible {
		os.Exit(1)
	}
}
