// Command rescheck checks a JSON spec for interleavings that terminate
// with unreleased resources.
//
// Usage: rescheck <spec.json>
//
// Exit codes: 0 = safe, 1 = violated (verified counterexample printed),
// 2 = unknown (budget exhausted), 3 = usage or spec error.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"rescheck"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: rescheck <spec.json>")
		os.Exit(3)
	}
	spec, err := rescheck.LoadSpec(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(3)
	}
	report, err := spec.Check()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(3)
	}
	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(3)
	}
	fmt.Println(string(out))
	switch report.Status {
	case rescheck.StatusViolated:
		os.Exit(1)
	case rescheck.StatusUnknown:
		os.Exit(2)
	}
}
