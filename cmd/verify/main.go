// Command verify is an independent satisfiability checker. Given an
// instance and a claimed minimal unsatisfiable subset (e.g. the report
// produced by diagnose), it proves by exhaustive enumeration that the
// subset has no satisfying assignment and that dropping any single
// member leaves a satisfiable set. It can also check the satisfiability
// of an arbitrary subset of constraints (-subset).
//
// Exit code: 0 if the claim holds, 1 if it is refuted, 2 on errors.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"

	"configdiag/exhaust"
	"configdiag/instance"
)

// musFile accepts the JSON report written by diagnose, or any JSON object
// with a "mus" array of constraint names.
type musFile struct {
	Status string   `json:"status"`
	MUS    []string `json:"mus"`
}

func main() {
	in := flag.String("in", "-", `instance JSON file ("-" reads stdin)`)
	mus := flag.String("mus", "", "MUS report JSON from diagnose: verify minimality")
	subset := flag.String("subset", "", "comma-separated constraint names: check satisfiability")
	max := flag.Int64("max-assignments", exhaust.DefaultMaxAssignments,
		"abort an enumeration beyond this many assignments")
	flag.Parse()

	if (*mus == "") == (*subset == "") {
		fmt.Fprintln(os.Stderr, "verify: provide exactly one of -mus or -subset")
		os.Exit(2)
	}

	inst, err := instance.Load(*in)
	if err != nil {
		fatal("loading instance", err)
	}

	if *subset != "" {
		names := strings.Split(*subset, ",")
		s, err := exhaust.Check(inst, names, *max)
		if err != nil {
			fatal("checking subset", err)
		}
		if s.Satisfying > 0 {
			fmt.Printf("SAT: %d of %d assignments satisfy %v; witness %s\n",
				s.Satisfying, s.Assignments, names, formatWitness(s.Witness))
		} else {
			fmt.Printf("UNSAT: 0 of %d assignments satisfy %v (proven by exhaustive enumeration)\n",
				s.Assignments, names)
			os.Exit(1)
		}
		return
	}

	names, err := readMUS(*mus)
	if err != nil {
		fatal("reading MUS", err)
	}
	v, err := exhaust.VerifyMUS(inst, names, *max)
	if err != nil {
		fatal("verifying", err)
	}
	printVerification(v)
	if !v.OK {
		os.Exit(1)
	}
}

func readMUS(path string) ([]string, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = ioutil.ReadAll(os.Stdin)
	} else {
		data, err = ioutil.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	var mf musFile
	if err := json.Unmarshal(data, &mf); err != nil {
		return nil, fmt.Errorf("invalid JSON: %v", err)
	}
	if len(mf.MUS) == 0 {
		return nil, fmt.Errorf("no constraints listed under \"mus\"")
	}
	return mf.MUS, nil
}

func printVerification(v *exhaust.Verification) {
	fmt.Printf("MUS under test: %s\n", strings.Join(v.MUS, ", "))
	if v.WholeUnsat {
		fmt.Printf("whole set: enumerated all %d assignments, 0 satisfy every constraint -> UNSATISFIABLE (proven exhaustively)\n",
			v.WholeAssignments)
	} else {
		fmt.Printf("whole set: found satisfying assignments among %d enumerated -> NOT unsatisfiable\n",
			v.WholeAssignments)
	}
	for _, r := range v.Removals {
		if r.Satisfiable {
			fmt.Printf("without %s: SAT (enumerated %d assignments), witness %s\n",
				r.Dropped, r.Assignments, formatWitness(r.Witness))
		} else {
			fmt.Printf("without %s: still UNSAT (enumerated %d assignments) -> %s is redundant\n",
				r.Dropped, r.Assignments, r.Dropped)
		}
	}
	if v.OK {
		fmt.Println("RESULT: OK - inclusion-minimal unsatisfiable subset confirmed")
	} else {
		fmt.Println("RESULT: FAILED - not an inclusion-minimal unsatisfiable subset")
	}
}

func formatWitness(w instance.Assignment) string {
	keys := make([]string, 0, len(w))
	for k := range w {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%q", k, w[k])
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func fatal(what string, err error) {
	fmt.Fprintf(os.Stderr, "verify: %s: %v\n", what, err)
	os.Exit(2)
}
