package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	inputPath := flag.String("input", "", "input JSON file (default: stdin)")
	flag.Parse()

	var r *os.File
	if *inputPath == "" {
		r = os.Stdin
	} else {
		f, err := os.Open(*inputPath)
		if err != nil {
			fatal("cannot open input: %v", err)
		}
		defer f.Close()
		r = f
	}

	in, err := readInput(r)
	if err != nil {
		fatal("bad input: %v", err)
	}
	res, err := Solve(in)
	if err != nil {
		fatal("solve: %v", err)
	}

	out, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		fatal("encode: %v", err)
	}
	fmt.Println(string(out))
	if res.Status != "optimal" {
		os.Exit(2)
	}
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
