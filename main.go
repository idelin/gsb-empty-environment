package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: ticket-analyzer [flags] <spec.json | - >

Analyzes a ticket routing spec with finite field domains. Rules match on
field values and may move the ticket to another queue and/or rewrite fields.
Execution picks any matching rule and stops when no rule matches.

The analyzer enumerates the full state space (it does NOT rely on static
rule-reference cycle checks) and reports:
  - whether every input terminates, with a minimal input + loop path if not;
  - whether the final queue is unique, with a minimal divergent input and
    the two execution paths if not.

Flags:
`)
	flag.PrintDefaults()
}

func readInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

func main() {
	jsonOut := flag.Bool("json", false, "emit a machine-readable JSON report")
	maxStates := flag.Int("max-states", 5000000, "safety cap on the number of enumerated states")
	flag.Usage = usage
	flag.Parse()
	if flag.NArg() != 1 {
		usage()
		os.Exit(2)
	}
	data, err := readInput(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot read spec: %v\n", err)
		os.Exit(2)
	}
	spec, err := parseSpec(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
	m, err := compileModel(spec, *maxStates)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
	g := buildGraph(m)
	rep := analyze(m, g)
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot encode report: %v\n", err)
			os.Exit(2)
		}
	} else {
		fmt.Print(renderText(m, rep))
	}
	if rep.Terminates && rep.Unique {
		os.Exit(0)
	}
	os.Exit(1)
}
