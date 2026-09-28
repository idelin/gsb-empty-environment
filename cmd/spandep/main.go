package main

import (
	"flag"
	"fmt"
	"os"

	"spandep"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "synth":
		fs := flag.NewFlagSet("synth", flag.ExitOnError)
		seed := fs.Int64("seed", 1, "deterministic seed for the synthesized telemetry")
		out := fs.String("out", "", "output fixture file (default: stdout)")
		fs.Parse(os.Args[2:])
		spans := spandep.Synthesize(*seed)
		w := os.Stdout
		if *out != "" {
			f, err := os.Create(*out)
			if err != nil {
				fatal(err)
			}
			defer f.Close()
			w = f
		}
		if err := spandep.WriteFixture(w, *seed, spans); err != nil {
			fatal(err)
		}
	case "replay":
		fs := flag.NewFlagSet("replay", flag.ExitOnError)
		fs.Parse(os.Args[2:])
		if fs.NArg() != 1 {
			fmt.Fprintln(os.Stderr, "replay requires exactly one fixture file")
			os.Exit(2)
		}
		f, err := os.Open(fs.Arg(0))
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		fx, err := spandep.LoadFixture(f)
		if err != nil {
			fatal(err)
		}
		spandep.RenderReport(os.Stdout, spandep.Build(fx.Spans))
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: spandep <synth|replay> [flags]")
	fmt.Fprintln(os.Stderr, "  synth  -seed N [-out file]   synthesize telemetry locally into a fixture")
	fmt.Fprintln(os.Stderr, "  replay <fixture>             rebuild dependency edges from a fixture")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
