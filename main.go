package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

// CLI output wraps the checker result with the independent replay verdict.
type output struct {
	Result
	ReplayVerified bool   `json:"replay_verified,omitempty"`
	ReplayError    string `json:"replay_error,omitempty"`
}

func main() {
	verify := flag.Bool("verify", true, "independently replay-verify counterexample traces")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: rescheck [-verify] spec.json\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	spec, err := LoadSpec(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}

	res := Check(spec)
	out := output{Result: res}

	if res.Status == StatusViolation && *verify {
		if err := VerifyCounterexample(spec, res); err != nil {
			out.ReplayError = err.Error()
		} else {
			out.ReplayVerified = true
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	switch res.Status {
	case StatusViolation:
		if *verify && !out.ReplayVerified {
			os.Exit(1)
		}
		os.Exit(10)
	case StatusUnknown:
		os.Exit(11)
	}
}
