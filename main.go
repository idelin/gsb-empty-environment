package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	file := flag.String("f", "-", "problem JSON file, or - for stdin")
	verify := flag.Bool("verify", false, "cross-check the solver against exhaustive policy enumeration")
	flag.Parse()

	p, err := LoadProblemFile(*file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	var res *Result
	var enum *EnumResult
	if *verify {
		var verr error
		res, enum, verr = p.Verify()
		if verr != nil {
			fmt.Fprintln(os.Stderr, "verification failed:", verr)
			os.Exit(2)
		}
		fmt.Printf("verified against %d enumerated policies (%d feasible)\n", enum.Total, enum.FeasibleCount)
	} else {
		var serr error
		res, serr = p.Solve()
		if serr != nil {
			fmt.Fprintln(os.Stderr, "error:", serr)
			os.Exit(1)
		}
	}

	if !res.Feasible {
		fmt.Println("infeasible: no policy satisfies the capacity constraints")
		os.Exit(3)
	}

	fmt.Printf("worst-case loss: %s\n", res.Worst.RatString())
	fmt.Printf("average loss:    %s\n", res.Avg.RatString())
	fmt.Println("policy:")
	for s, sig := range p.Signals() {
		fmt.Printf("  signal %-12s -> action %s\n", sig, p.Actions[res.Policy[s]])
	}
	fmt.Println("capacity usage:")
	for a, name := range p.Actions {
		cap := "inf"
		if p.Capacity[a] != nil {
			cap = p.Capacity[a].RatString()
		}
		fmt.Printf("  action %-12s used %s / %s\n", name, res.Usage[a].RatString(), cap)
	}
}
