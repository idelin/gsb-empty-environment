package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	strict := flag.Bool("strict", false, "stop at the first operation the kernel would reject")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [--strict] [trace-file]\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "  Reads a trace of exec/capset/prctl operations (stdin if no file)")
		fmt.Fprintln(os.Stderr, "  and replays the capabilities(7) set transformations for a non-root thread.")
	}
	flag.Parse()

	var input *os.File
	switch flag.NArg() {
	case 0:
		input = os.Stdin
	case 1:
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		input = f
	default:
		flag.Usage()
		os.Exit(2)
	}

	events, err := parseTrace(input)
	if err != nil {
		fatal(err)
	}
	if err := runTrace(os.Stdout, events, *strict); err != nil {
		os.Exit(1)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "capreplay:", err)
	os.Exit(1)
}
