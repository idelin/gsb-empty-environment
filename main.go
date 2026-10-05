package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
)

func usage() {
	fmt.Fprintf(os.Stderr, "usage:\n  strsolver solve <problem.json>\n  strsolver verify <problem.json> <solution.json>\n")
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(64)
	}
	switch os.Args[1] {
	case "solve":
		if len(os.Args) != 3 {
			usage()
			os.Exit(64)
		}
		runSolve(os.Args[2])
	case "verify":
		if len(os.Args) != 4 {
			usage()
			os.Exit(64)
		}
		runVerify(os.Args[2], os.Args[3])
	default:
		usage()
		os.Exit(64)
	}
}

func runSolve(path string) {
	p, err := LoadProblem(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
	sol := Solve(p)
	out, err := json.MarshalIndent(sol, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
	fmt.Println(string(out))
}

func runVerify(problemPath, solutionPath string) {
	p, err := LoadProblem(problemPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
	data, err := ioutil.ReadFile(solutionPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
	var sol Solution
	if err := json.Unmarshal(data, &sol); err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid solution JSON: %v\n", err)
		os.Exit(2)
	}
	assignment := sol.Assignment
	if assignment == nil {
		// Also accept a bare {name: value} map.
		var raw map[string]string
		if err := json.Unmarshal(data, &raw); err == nil {
			assignment = raw
		}
	}
	if err := Verify(p, assignment); err != nil {
		fmt.Printf("FAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("OK")
}
