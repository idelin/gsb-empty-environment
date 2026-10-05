package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
)

// markov reads a chain description (JSON, file argument or stdin) and prints
// the full analysis as JSON:
//
//	{
//	  "states": 4,
//	  "absorbing": [3],
//	  "transitions": [
//	    {"from": 0, "to": 1, "prob": "1/3"},
//	    {"from": 0, "to": 3, "prob": "2/3"}
//	  ]
//	}
//
// Absorbing states implicitly self-loop with probability 1 and must not
// declare outgoing transitions. All probabilities are exact rationals.
func main() {
	var data []byte
	var err error
	if len(os.Args) > 1 {
		data, err = ioutil.ReadFile(os.Args[1])
	} else {
		data, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fatal(err)
	}
	chain, err := ParseChain(data)
	if err != nil {
		fatal(err)
	}
	out, err := json.MarshalIndent(Analyze(chain), "", "  ")
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(out))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
