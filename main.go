package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
)

func usage() {
	fmt.Fprintf(os.Stderr, `invcheck - bounded loop-invariant checker for integer affine programs

Usage: invcheck [flags] [input.json]   (reads stdin when no file is given)

Input JSON:
  variables  ["x", "y"]                    variable names
  bounds     {"min": -10, "max": 10,       global inclusive range
              "vars": {"x": {"min": 0, "max": 5}}}   optional per-var override
  init       [constraint, ...]             constraints on initial states
  guard      [constraint, ...]             loop guard (conjunction)
  assign     [{"var": "x", "coeffs": {"x": 1}, "const": 1}]  parallel affine updates
  invariant  [constraint, ...]             candidate invariant (conjunction)
  post       [constraint, ...]             postcondition (conjunction)

constraint = {"coeffs": {"x": 2, "y": -1}, "op": "<=", "const": 7}
             meaning 2*x - 1*y <= 7; op in <=, <, >=, >, ==, !=

Exit codes: 0 = PROVED, 1 = FAILED, 2 = INDETERMINATE, 3 = input/usage error
`)
}

func main() {
	maxStates := flag.Int64("max-states", 10000000, "maximum number of states to enumerate")
	compact := flag.Bool("compact", false, "emit compact JSON instead of indented")
	flag.Usage = usage
	flag.Parse()

	var data []byte
	var err error
	if flag.NArg() > 1 {
		usage()
		os.Exit(3)
	} else if flag.NArg() == 1 {
		data, err = ioutil.ReadFile(flag.Arg(0))
	} else {
		data, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading input: %v\n", err)
		os.Exit(3)
	}

	var prog Program
	if err := json.Unmarshal(data, &prog); err != nil {
		fmt.Fprintf(os.Stderr, "error parsing input: %v\n", err)
		os.Exit(3)
	}
	if err := prog.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "invalid program: %v\n", err)
		os.Exit(3)
	}

	rep, err := prog.Check(*maxStates)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(3)
	}

	var out []byte
	if *compact {
		out, err = json.Marshal(rep)
	} else {
		out, err = json.MarshalIndent(rep, "", "  ")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error encoding report: %v\n", err)
		os.Exit(3)
	}
	fmt.Println(string(out))

	switch rep.Verdict {
	case Proved:
		os.Exit(0)
	case Failed:
		os.Exit(1)
	default:
		os.Exit(2)
	}
}
