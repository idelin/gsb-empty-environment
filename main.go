package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	scriptPath := flag.String("script", "", "path to event script (default: read from stdin)")
	flag.Parse()

	var r io.Reader = os.Stdin
	if *scriptPath != "" {
		f, err := os.Open(*scriptPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		defer f.Close()
		r = f
	}

	script, err := ParseScript(r)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse error:", err)
		os.Exit(1)
	}

	sim := NewSim(script.Tuples, script.Delay, script.TimeWait)
	for _, c := range script.Commands {
		sim.Submit(c)
	}
	sim.Run()
	fmt.Print(RenderReport(sim.Obs))
}
