package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"math"
	"os"
)

func main() {
	input := flag.String("i", "", "input JSON file (default: stdin)")
	brute := flag.String("brute", "auto", "exhaustive cross-check: auto|always|never")
	bruteLimit := flag.Int64("brute-limit", 2000000, "max assignments enumerated by the auto cross-check")
	flag.Parse()

	var data []byte
	var err error
	if *input == "" {
		data, err = ioutil.ReadAll(os.Stdin)
	} else {
		data, err = ioutil.ReadFile(*input)
	}
	if err != nil {
		fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		fatal(fmt.Errorf("invalid JSON: %v", err))
	}
	prob, err := NewProblem(&cfg)
	if err != nil {
		fatal(err)
	}

	sol := prob.Solve()
	printSolution(prob, sol)

	var limit int64
	switch *brute {
	case "always":
		limit = math.MaxInt64
	case "auto":
		limit = *bruteLimit
	case "never":
		return
	default:
		fatal(fmt.Errorf("unknown -brute mode %q (want auto|always|never)", *brute))
	}
	bf, count, ok := prob.BruteForce(limit)
	if !ok {
		fmt.Printf("\nExhaustive cross-check: skipped, more than %d assignments\n", limit)
		return
	}
	match := bf.cost == sol.cost && prob.prefixCmp(bf.assign, sol.assign, prob.size) == 0
	fmt.Printf("\nExhaustive cross-check: enumerated %d assignments, optimal penalty %d", count, bf.cost)
	if match {
		fmt.Println(" -- matches solver (cost and tie-break)")
	} else {
		fmt.Println(" -- MISMATCH with solver!")
		os.Exit(1)
	}
}

func printSolution(p *Problem, sol Solution) {
	fmt.Printf("Bracket (size %d, %d rounds), total penalty %d:\n", p.size, p.rounds, sol.cost)
	for s, pi := range sol.assign {
		if pi < 0 {
			fmt.Printf("  slot %2d: bye\n", s)
			continue
		}
		pl := p.players[pi]
		lock := ""
		if p.locked[s] == pi {
			lock = " [locked]"
		}
		fmt.Printf("  slot %2d: #%-3d team %-4s tier %d%s\n", s, pl.ID, pl.Team, pl.Tier, lock)
	}
	pairs := p.PairReports(sol)
	if len(pairs) == 0 {
		fmt.Println("No same-team pairs.")
		return
	}
	fmt.Println("Same-team pairs, earliest meeting round:")
	for _, pr := range pairs {
		fmt.Printf("  team %-4s #%-3d vs #%-3d -> round %d/%d (%s), penalty %d\n",
			pr.Team, pr.ID1, pr.ID2, pr.Round, p.rounds, p.RoundLabel(pr.Round), pr.Penalty)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
