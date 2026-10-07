// Command policysolve reads a decision problem from a JSON file, computes an
// optimal policy tree (minimizing worst-case total loss, tie-broken by
// expected total loss), and prints the tree together with a per-world replay.
//
// Usage:
//
//	policysolve problem.json
//	policysolve -brute problem.json   # also cross-check via exhaustive enumeration
package main

import (
	"flag"
	"fmt"
	"os"

	"policytree"
)

func main() {
	brute := flag.Bool("brute", false, "cross-check the optimum by exhaustively enumerating all policy trees")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: policysolve [-brute] problem.json")
		os.Exit(2)
	}
	f, err := os.Open(flag.Arg(0))
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	p, err := policytree.Load(f)
	if err != nil {
		fatal(err)
	}

	cost, tree, err := policytree.Solve(p)
	if err != nil {
		fatal(err)
	}

	fmt.Println("optimal policy tree:")
	fmt.Print(tree.String(p))
	fmt.Println("optimal cost:", cost)

	if err := policytree.VerifyNonAnticipation(p, tree); err != nil {
		fatal(fmt.Errorf("non-anticipation check failed: %w", err))
	}
	fmt.Println("non-anticipation check: ok (action is a function of observation history)")

	entries, err := policytree.Replay(p, tree)
	if err != nil {
		fatal(err)
	}
	fmt.Println("per-world replay:")
	for _, e := range entries {
		fmt.Println(" ", e)
	}

	if *brute {
		best, err := policytree.BruteForce(p)
		if err != nil {
			fatal(err)
		}
		fmt.Println("exhaustive enumeration optimum:", best)
		if best.Worst != cost.Worst || best.Exp.Cmp(cost.Exp) != 0 {
			fatal(fmt.Errorf("MISMATCH: solver found %s but enumeration found %s", cost, best))
		}
		fmt.Println("cross-check: ok (solver matches exhaustive enumeration)")
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
