// musdiag diagnoses unsatisfiable finite-domain constraint sets.
//
//	musdiag -file problem.txt [-limit N]
//
// If the constraint set is satisfiable it prints a witness. Otherwise it
// reduces the set to an inclusion-minimal unsatisfiable subset (MUS) via
// a deterministic deletion pass in input order, then proves minimality by
// exhibiting a satisfying assignment for every single-constraint deletion.
package main

import (
	"flag"
	"fmt"
	"os"

	"musdiag/csp"
	"musdiag/solver"
)

func main() {
	file := flag.String("file", "", "path to the constraint problem file (required)")
	limit := flag.Int64("limit", solver.DefaultLimit, "maximum number of complete assignments to enumerate")
	flag.Parse()
	if *file == "" {
		fmt.Fprintln(os.Stderr, "usage: musdiag -file problem.txt [-limit N]")
		os.Exit(2)
	}
	f, err := os.Open(*file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	defer f.Close()
	p, err := csp.Parse(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse error:", err)
		os.Exit(2)
	}

	fmt.Printf("problem: %d variables, %d constraints, %d complete assignments (limit %d)\n",
		len(p.Vars), len(p.Cons), p.Combinations(*limit), *limit)

	mus, witness, res := solver.FindMUS(p, *limit)
	switch res {
	case solver.SAT:
		fmt.Println("SATISFIABLE: all constraints hold simultaneously")
		fmt.Println("witness:", csp.FormatEnv(witness))
		return
	case solver.Unknown:
		fmt.Println("UNKNOWN: assignment space exceeds the enumeration limit; raise -limit")
		os.Exit(3)
	}

	fmt.Printf("UNSATISFIABLE: all %d constraints cannot hold simultaneously\n", len(p.Cons))
	fmt.Printf("minimal unsatisfiable subset (%d of %d constraints, deletion pass in input order):\n",
		len(mus), len(p.Cons))
	for _, c := range mus {
		fmt.Println("  " + c.String())
	}

	witnesses, ok := solver.VerifyMUS(p, mus, *limit)
	if !ok {
		fmt.Fprintln(os.Stderr, "internal error: minimality verification failed")
		os.Exit(1)
	}
	fmt.Println("minimality proof (removing any one constraint makes it satisfiable):")
	for _, c := range mus {
		fmt.Printf("  without %s: SAT, e.g. %s\n", c.Name, csp.FormatEnv(witnesses[c.Name]))
	}
}
