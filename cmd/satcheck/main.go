// satcheck is a standalone satisfiability checker for finite-domain
// constraint sets. It proves claims by plain exhaustive enumeration with
// its own enumerator (it does not share search code with musdiag):
//
//	satcheck -file problem.txt                 # is the whole set SAT?
//	satcheck -file problem.txt -subset c1,c3   # prove subset UNSAT and
//	                                           # every single deletion SAT
//
// With -subset, exit status 0 means the subset was proven unsatisfiable by
// complete enumeration AND every one-constraint deletion was shown
// satisfiable with an explicit witness, i.e. the subset is an
// inclusion-minimal unsatisfiable set. Exit status 1 means a claim failed;
// 2 means a usage/parse error; 3 means the enumeration limit was exceeded.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"musdiag/csp"
)

const defaultLimit int64 = 100000000

// enumerate is satcheck's own exhaustive enumerator: no pruning, every
// complete assignment in the Cartesian product of the domains is visited
// in variable declaration order. It returns the first assignment (in that
// order) satisfying all constraints, plus the number of complete
// assignments examined.
func enumerate(p *csp.Problem, cons []csp.Constraint) (map[string]int, int64) {
	env := make(map[string]int, len(p.Vars))
	var examined int64
	vars := p.Vars
	var walk func(i int) bool
	walk = func(i int) bool {
		if i == len(vars) {
			examined++
			for _, c := range cons {
				if !c.Holds(env) {
					return false
				}
			}
			return true
		}
		v := vars[i]
		for val := v.Lo; val <= v.Hi; val++ {
			env[v.Name] = val
			if walk(i + 1) {
				return true
			}
		}
		delete(env, v.Name)
		return false
	}
	if walk(0) {
		return env, examined
	}
	return nil, examined
}

func main() {
	file := flag.String("file", "", "path to the constraint problem file (required)")
	subset := flag.String("subset", "", "comma-separated constraint names forming the alleged minimal unsatisfiable subset")
	limit := flag.Int64("limit", defaultLimit, "maximum number of complete assignments to enumerate")
	flag.Parse()
	if *file == "" {
		fmt.Fprintln(os.Stderr, "usage: satcheck -file problem.txt [-subset c1,c3,...] [-limit N]")
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
	if p.Combinations(*limit) > *limit {
		fmt.Println("UNKNOWN: assignment space exceeds the enumeration limit; raise -limit")
		os.Exit(3)
	}

	cons := p.Cons
	if *subset != "" {
		byName := map[string]csp.Constraint{}
		for _, c := range p.Cons {
			byName[c.Name] = c
		}
		cons = nil
		for _, name := range strings.Split(*subset, ",") {
			name = strings.TrimSpace(name)
			c, ok := byName[name]
			if !ok {
				fmt.Fprintf(os.Stderr, "error: no constraint named %q\n", name)
				os.Exit(2)
			}
			cons = append(cons, c)
		}
	}

	names := make([]string, len(cons))
	for i, c := range cons {
		names[i] = c.Name
	}
	fmt.Printf("checking {%s} over %d complete assignments\n",
		strings.Join(names, ", "), p.Combinations(*limit))

	env, examined := enumerate(p, cons)
	if env != nil {
		fmt.Printf("SATISFIABLE after %d complete assignments; witness: %s\n",
			examined, csp.FormatEnv(env))
		if *subset != "" {
			fmt.Println("CLAIM FAILED: the alleged unsatisfiable subset is satisfiable")
			os.Exit(1)
		}
		return
	}
	fmt.Printf("PROVEN UNSATISFIABLE: all %d complete assignments enumerated, none satisfies the set\n",
		examined)

	if *subset == "" {
		return
	}
	// Minimality: every single-constraint deletion must be satisfiable.
	for _, drop := range cons {
		sub := make([]csp.Constraint, 0, len(cons)-1)
		for _, c := range cons {
			if c.Name != drop.Name {
				sub = append(sub, c)
			}
		}
		env, examined := enumerate(p, sub)
		if env == nil {
			fmt.Printf("CLAIM FAILED: {%s} minus %s is still UNSAT, so %s is redundant\n",
				strings.Join(names, ", "), drop.Name, drop.Name)
			os.Exit(1)
		}
		fmt.Printf("  minus %s: SAT after %d assignments, witness: %s\n",
			drop.Name, examined, csp.FormatEnv(env))
	}
	fmt.Println("MINIMALITY PROVEN: removing any single constraint makes the set satisfiable")
}
