// Command dtri reads survey points as JSON, triangulates them with exact
// integer arithmetic, and writes the canonical triangulation as JSON.
//
// Usage:
//
//	dtri [-in file] [-out file] [-validate]
//
// Defaults: read stdin, write stdout. With -validate the output is checked
// by the independent validator before being written; any violation is
// reported on stderr and the exit code is non-zero.
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	"dtri"
)

func main() {
	in := flag.String("in", "", "input JSON file (default stdin)")
	out := flag.String("out", "", "output JSON file (default stdout)")
	validate := flag.Bool("validate", false, "run the independent validator on the result")
	flag.Parse()

	var data []byte
	var err error
	if *in == "" {
		data, err = ioutil.ReadAll(os.Stdin)
	} else {
		data, err = ioutil.ReadFile(*in)
	}
	if err != nil {
		die("read input: %v", err)
	}

	pts, err := dtri.DecodeInput(data)
	if err != nil {
		die("parse input: %v", err)
	}

	res, err := dtri.Triangulate(pts)
	if err != nil {
		die("triangulate: %v", err)
	}

	if *validate {
		if err := dtri.Validate(res); err != nil {
			die("validation failed: %v", err)
		}
	}

	enc, err := dtri.EncodeOutput(res)
	if err != nil {
		die("encode output: %v", err)
	}
	enc = append(enc, '\n')

	if *out == "" {
		if _, err := os.Stdout.Write(enc); err != nil {
			die("write output: %v", err)
		}
	} else if err := ioutil.WriteFile(*out, enc, 0644); err != nil {
		die("write output: %v", err)
	}
}

func die(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "dtri: "+format+"\n", args...)
	os.Exit(1)
}
