package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// maxExploredShown caps how many explored states are printed for an
// unreachable goal; the full count is always reported.
const maxExploredShown = 100

func usage() {
	fmt.Fprintf(os.Stderr, `finite-state optimal planner (up to %d propositions)

usage:
  planner solve  [-plan out.txt] <problem.txt>
  planner replay <problem.txt> <plan.txt>

`, MaxProps)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "solve":
		cmdSolve(os.Args[2:])
	case "replay":
		cmdReplay(os.Args[2:])
	default:
		usage()
	}
}

func cmdSolve(args []string) {
	fs := flag.NewFlagSet("solve", flag.ExitOnError)
	planOut := fs.String("plan", "", "write the plan (one action per line) to this file")
	fs.Parse(args)
	if fs.NArg() != 1 {
		usage()
	}
	p, err := ParseFile(fs.Arg(0))
	if err != nil {
		fatal("parse error: %v", err)
	}

	res := Solve(p)
	if !res.Reached {
		fmt.Printf("GOAL UNREACHABLE after exploring %d state(s):\n", len(res.Explored))
		shown := res.Explored
		if len(shown) > maxExploredShown {
			shown = shown[:maxExploredShown]
		}
		for _, e := range shown {
			fmt.Printf("  cost=%-4d %s\n", e.Cost, p.StateString(e.State))
		}
		if len(res.Explored) > len(shown) {
			fmt.Printf("  ... and %d more\n", len(res.Explored)-len(shown))
		}
		os.Exit(1)
	}

	fmt.Printf("GOAL REACHED, minimum cost %d, plan length %d:\n", res.Cost, len(res.Plan))
	for i, name := range res.Plan {
		fmt.Printf("  %d. %s\n", i+1, name)
	}
	if *planOut != "" {
		content := strings.Join(res.Plan, "\n")
		if content != "" {
			content += "\n"
		}
		if err := writeFile(*planOut, content); err != nil {
			fatal("cannot write plan: %v", err)
		}
		fmt.Printf("plan written to %s\n", *planOut)
	}
}

func cmdReplay(args []string) {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() != 2 {
		usage()
	}
	p, err := ParseFile(fs.Arg(0))
	if err != nil {
		fatal("parse error: %v", err)
	}
	plan, err := ReadPlan(fs.Arg(1))
	if err != nil {
		fatal("cannot read plan: %v", err)
	}
	if err := Replay(p, plan, os.Stdout); err != nil {
		fatal("REPLAY FAILED: %v", err)
	}
	fmt.Println("REPLAY OK")
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
