package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	inPath := flag.String("f", "", "input JSON file (default: stdin)")
	outPath := flag.String("o", "", "output JSON file (default: stdout)")
	compact := flag.Bool("compact", false, "emit compact JSON instead of indented")
	flag.Parse()

	var data []byte
	var err error
	if *inPath == "" || *inPath == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(*inPath)
	}
	if err != nil {
		fatal("reading input: %v", err)
	}

	inputs, err := parseInputs(data)
	if err != nil {
		fatal("%v", err)
	}

	res, err := trim(inputs)
	if err != nil {
		fatal("%v", err)
	}

	var out []byte
	if *compact {
		out, err = json.Marshal(res)
	} else {
		out, err = json.MarshalIndent(res, "", "  ")
	}
	if err != nil {
		fatal("encoding result: %v", err)
	}
	out = append(out, '\n')

	if *outPath == "" || *outPath == "-" {
		if _, err := os.Stdout.Write(out); err != nil {
			fatal("writing output: %v", err)
		}
	} else if err := os.WriteFile(*outPath, out, 0o644); err != nil {
		fatal("writing output: %v", err)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "corputrim: "+format+"\n", args...)
	os.Exit(1)
}
