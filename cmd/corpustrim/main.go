// corpustrim reads a corpus of inputs (JSON) and prints the exact minimal
// kept set: fewest samples first, then lowest total execution cost, while
// preserving all observed coverage edges and failure categories.
//
// Usage:
//   corpustrim [file]        read JSON from file (default: stdin)
//
// Input JSON:  {"inputs": [{"id": "...", "content": "...", "cost": 3,
//               "coverage": ["edge-a", ...],
//               "failure_categories": ["crash", ...]}, ...]}
// Duplicate inputs (same content) are merged; their observed coverage and
// failure categories are unioned, the cheapest cost is kept.
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"

	"corpustrim"
)

func main() {
	var data []byte
	var err error
	if len(os.Args) > 2 {
		fmt.Fprintln(os.Stderr, "usage: corpustrim [input.json]")
		os.Exit(2)
	}
	if len(os.Args) == 2 {
		data, err = ioutil.ReadFile(os.Args[1])
	} else {
		data, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "read input:", err)
		os.Exit(1)
	}
	var doc struct {
		Inputs []corpustrim.Input `json:"inputs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		fmt.Fprintln(os.Stderr, "parse input:", err)
		os.Exit(1)
	}
	res, err := corpustrim.Trim(doc.Inputs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "trim:", err)
		os.Exit(1)
	}
	out, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "encode result:", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}
