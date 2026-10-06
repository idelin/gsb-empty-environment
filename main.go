package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
)

// CLI: reads an Instance as JSON from stdin (or a file argument) and
// prints the optimal Result as JSON. With -verify it also runs the
// exhaustive brute-force enumerator and checks both solvers agree.
func main() {
	verify := flag.Bool("verify", false, "cross-check against exhaustive enumeration")
	flag.Parse()

	var data []byte
	var err error
	if flag.NArg() > 0 {
		data, err = ioutil.ReadFile(flag.Arg(0))
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
	res, err := Solve(&inst)
	if err != nil {
		fatal(err)
	}
	if *verify {
		ref, err := bruteForce(&inst)
		if err != nil {
			fatal(fmt.Errorf("brute force: %v", err))
		}
		if ref.Cost != res.Cost || !sameSchedule(ref.Schedule, res.Schedule) {
			fatal(fmt.Errorf("mismatch: dp cost=%d schedule=%v, brute force cost=%d schedule=%v",
				res.Cost, res.Schedule, ref.Cost, ref.Schedule))
		}
		fmt.Fprintln(os.Stderr, "verified: dp and brute force agree")
	}
	out, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(out))
}

func sameSchedule(a, b []int) bool {
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
	os.Exit(1)
}
