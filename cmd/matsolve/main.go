// Command matsolve reads a procurement problem as JSON (from a file argument
// or stdin) and prints a minimum-cost purchase and consumption plan.
//
//	matsolve problem.json
//	matsolve --json < problem.json
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"matsolve/solver"
)

func main() {
	jsonOut := flag.Bool("json", false, "print the solution as JSON")
	flag.Parse()

	var data []byte
	var err error
	if flag.NArg() > 0 {
		data, err = os.ReadFile(flag.Arg(0))
	} else {
		data, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "read input: %v\n", err)
		os.Exit(1)
	}
	var p solver.Problem
	if err := json.Unmarshal(data, &p); err != nil {
		fmt.Fprintf(os.Stderr, "parse input: %v\n", err)
		os.Exit(1)
	}

	sol, err := solver.Solve(&p)
	if err != nil {
		var gc *solver.GainCycleError
		if errors.As(err, &gc) {
			fmt.Fprintf(os.Stderr, "rejected: %v\n", err)
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if *jsonOut {
		out, _ := json.MarshalIndent(sol, "", "  ")
		fmt.Println(string(out))
		return
	}
	fmt.Print(sol.String())
}
