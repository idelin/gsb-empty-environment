package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

// restoreplanner reads a JSON restore-planning request and prints a JSON
// solution. Usage:
//
//	restoreplanner [-budget N] [-input file] [-pretty]
//	restoreplanner -verify solution.json [-input file]
//
// The budget counts expanded search nodes; -1 (default) means unlimited.
// Exit codes: 0 optimal/feasible/unknown, 1 infeasible or failed verify,
// 2 invalid input or usage.
func main() {
	budget := flag.Int64("budget", -1, "node expansion budget (-1 = unlimited)")
	inputPath := flag.String("input", "", "input JSON file (default: stdin)")
	verifyPath := flag.String("verify", "", "verify a solution JSON file instead of solving")
	pretty := flag.Bool("pretty", false, "pretty-print JSON output")
	flag.Parse()

	inData, err := readAll(*inputPath)
	if err != nil {
		fatal(err)
	}
	var in Input
	if err := json.Unmarshal(inData, &in); err != nil {
		fatal(fmt.Errorf("invalid input JSON: %w", err))
	}

	enc := json.NewEncoder(os.Stdout)
	if *pretty {
		enc.SetIndent("", "  ")
	}

	if *verifyPath != "" {
		os.Exit(runVerify(enc, &in, *verifyPath))
	}

	m, diags, bad := buildModel(&in)
	if bad {
		enc.Encode(Solution{Status: "error", Diagnostics: diags})
		os.Exit(2)
	}
	sol := Solve(m, diags, *budget)
	enc.Encode(sol)
	if sol.Status == StatusInfeasible {
		os.Exit(1)
	}
}

func runVerify(enc *json.Encoder, in *Input, path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		fatal(err)
	}
	var sol Solution
	if err := json.Unmarshal(data, &sol); err != nil {
		fatal(fmt.Errorf("invalid solution JSON: %w", err))
	}
	v := Verify(in, sol.Files)
	if v.OK && sol.Cost != "" && sol.Cost != v.Cost {
		v.OK = false
		v.Problems = append(v.Problems, fmt.Sprintf("claimed cost %s does not match recomputed cost %s", sol.Cost, v.Cost))
	}
	enc.Encode(v)
	if !v.OK {
		return 1
	}
	return 0
}

func readAll(path string) ([]byte, error) {
	if path == "" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "restoreplanner:", err)
	os.Exit(2)
}
