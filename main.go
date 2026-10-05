package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Input is the tool's JSON input document.
type Input struct {
	Events []Event `json:"events"`
}

func main() {
	var r io.Reader = os.Stdin
	if len(os.Args) > 1 {
		f, err := os.Open(os.Args[1])
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		r = f
	}
	var in Input
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		fatal(fmt.Errorf("decoding input: %w", err))
	}
	res := Reconcile(in.Events)
	out := json.NewEncoder(os.Stdout)
	out.SetIndent("", "  ")
	if err := out.Encode(res); err != nil {
		fatal(err)
	}
	switch res.Status {
	case "unique":
		os.Exit(0)
	case "ambiguous":
		os.Exit(2)
	default: // contradiction
		os.Exit(1)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(64)
}
