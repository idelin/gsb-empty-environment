package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	"forensics/planner"
)

func main() {
	specPath := flag.String("spec", "", "path to the JSON spec file (required)")
	asOf := flag.String("asof", "", "override the plan date (YYYY-MM-DD)")
	jsonOut := flag.Bool("json", false, "emit machine-readable JSON report")
	flag.Parse()

	if *specPath == "" {
		fmt.Fprintln(os.Stderr, "usage: forensics -spec spec.json [-asof YYYY-MM-DD] [-json]")
		os.Exit(2)
	}

	data, err := ioutil.ReadFile(*specPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read spec: %v\n", err)
		os.Exit(1)
	}
	var spec planner.Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		fmt.Fprintf(os.Stderr, "parse spec: %v\n", err)
		os.Exit(1)
	}
	if *asOf != "" {
		spec.AsOf = *asOf
	}

	inst, err := planner.Compile(&spec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid spec: %v\n", err)
		os.Exit(1)
	}

	plan := planner.Solve(inst)

	if *jsonOut {
		out, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "encode report: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(out))
	} else {
		fmt.Print(planner.Report(inst, plan))
	}

	if !plan.Feasible {
		os.Exit(1)
	}
}
