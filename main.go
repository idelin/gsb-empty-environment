package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	input := flag.String("input", "", "path to scenario JSON file")
	ticks := flag.Int("ticks", 0, "override simulation ticks (0 = use scenario value, default 200)")
	flag.Parse()
	if *input == "" {
		fmt.Fprintln(os.Stderr, "usage: netsim -input scenario.json [-ticks N]")
		os.Exit(2)
	}
	sc, err := LoadScenario(*input)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if *ticks > 0 {
		sc.SimTicks = *ticks
	}
	if sc.SimTicks <= 0 {
		sc.SimTicks = 200
	}
	fmt.Print(Validate(sc))
}
