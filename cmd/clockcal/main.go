// clockcal reads a multi-channel clock calibration problem as JSON from a
// file (or stdin) and writes the analysis report as JSON to stdout.
//
// Usage:
//
//	clockcal [input.json]
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"

	"clockcal/calib"
)

func main() {
	var data []byte
	var err error
	if len(os.Args) > 1 {
		data, err = ioutil.ReadFile(os.Args[1])
	} else {
		data, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "clockcal: read input:", err)
		os.Exit(1)
	}
	in, err := calib.DecodeInput(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "clockcal: parse input:", err)
		os.Exit(1)
	}
	out, err := json.MarshalIndent(calib.Analyze(in), "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "clockcal: encode report:", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}
