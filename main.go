package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
)

func main() {
	file := flag.String("f", "", "input JSON file (default: stdin)")
	verify := flag.Bool("verify", false, "cross-check against exhaustive enumeration (small instances only)")
	flag.Parse()

	var data []byte
	var err error
	if *file != "" {
		data, err = ioutil.ReadFile(*file)
	} else {
		data, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fatal(err)
	}
	var inst Instance
	if err := json.Unmarshal(data, &inst); err != nil {
		fatal(err)
	}

	res := Solve(&inst)
	out := map[string]interface{}{}
	dec, _ := json.Marshal(res)
	json.Unmarshal(dec, &out)

	if *verify {
		brute := BruteSolve(&inst)
		match := brute.Status == res.Status &&
			brute.Makespan == res.Makespan &&
			equalInts(brute.Starts, res.Starts)
		out["verify"] = map[string]interface{}{
			"bruteStatus":   brute.Status,
			"bruteMakespan": brute.Makespan,
			"bruteStarts":   brute.Starts,
			"match":         match,
		}
		if !match {
			out["status"] = "mismatch"
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fatal(err)
	}
	if res.Status != "optimal" {
		os.Exit(1)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(2)
}
