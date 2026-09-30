package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	inputPath := flag.String("input", "", "path to scenario JSON (required)")
	ticks := flag.Int("ticks", 0, "override simulation ticks (0 = use input/default)")
	noSim := flag.Bool("no-sim", false, "skip the discrete-event simulation")
	flag.Parse()

	if *inputPath == "" {
		fmt.Fprintln(os.Stderr, "usage: netverify -input scenario.json [-ticks N] [-no-sim]")
		os.Exit(2)
	}
	in, err := LoadInput(*inputPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	n, err := BuildNetwork(in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid input:", err)
		os.Exit(1)
	}
	if *ticks > 0 {
		n.SimTicks = *ticks
	}

	fmt.Printf("network %q: %d links, %d flows, %d failure scenarios\n",
		n.Name, len(n.Links), len(n.Flows), len(n.Scenarios))

	// 1. Baseline static capacity check.
	base := analyzeScenario(n, "", nil)
	writeScenarioReport(os.Stdout, n, base)

	// 2. Redundant-path condition per flow.
	writeRedundancy(os.Stdout, checkRedundancy(n))

	// 3. Failure scenarios: unreachable flows + residual capacity.
	for _, sc := range n.Scenarios {
		rep := analyzeScenario(n, sc.ID, n.FailedSet(sc))
		writeScenarioReport(os.Stdout, n, rep)
	}

	// 4. Discrete-event simulation (integer ticks, no real traffic).
	if !*noSim {
		writeSimResult(os.Stdout, Simulate(n, "", nil, n.SimTicks))
		for _, sc := range n.Scenarios {
			writeSimResult(os.Stdout, Simulate(n, sc.ID, n.FailedSet(sc), n.SimTicks))
		}
	}

	if base.AnyOverload {
		os.Exit(1)
	}
}
