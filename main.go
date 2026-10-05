package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

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
		fatal(fmt.Errorf("invalid input JSON: %v", err))
	}
	res, err := Solve(&in)
	if err != nil {
		fatal(err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
