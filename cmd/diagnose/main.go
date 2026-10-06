// Command diagnose reads a finite-domain constraint instance, reduces an
// unsatisfiable constraint set to an inclusion-minimal unsatisfiable
// subset (MUS) by deletion in input order, and writes a JSON report.
//
// The human-readable reduction trace goes to stderr; the JSON report
// (suitable as input for the verify command) goes to -out.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	"configdiag/diag"
	"configdiag/instance"
)

func main() {
	in := flag.String("in", "-", `instance JSON file ("-" reads stdin)`)
	out := flag.String("out", "-", `report output file ("-" writes stdout)`)
	flag.Parse()

	inst, err := instance.Load(*in)
	if err != nil {
		fatal("loading instance", err)
	}
	res, err := diag.FindMUS(inst)
	if err != nil {
		fatal("diagnosing", err)
	}

	for _, s := range res.Steps {
		verb := "drop"
		if s.Kept {
			verb = "keep"
		}
		fmt.Fprintf(os.Stderr, "%-4s %-24s %s\n", verb, s.Constraint, s.Reason)
	}
	if res.Status == "satisfiable" {
		fmt.Fprintln(os.Stderr, "instance is satisfiable: no unsatisfiable subset exists")
	} else {
		fmt.Fprintf(os.Stderr, "minimal unsatisfiable subset: %v\n", res.MUS)
	}

	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		fatal("encoding report", err)
	}
	data = append(data, '\n')
	if *out == "-" {
		if _, err := os.Stdout.Write(data); err != nil {
			fatal("writing report", err)
		}
	} else if err := ioutil.WriteFile(*out, data, 0644); err != nil {
		fatal("writing report", err)
	}
}

func fatal(what string, err error) {
	fmt.Fprintf(os.Stderr, "diagnose: %s: %v\n", what, err)
	os.Exit(2)
}
