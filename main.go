package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
)

func main() {
	file := flag.String("f", "", "instance JSON file (default: stdin)")
	verify := flag.Bool("verify", false, "cross-check the solver against exhaustive enumeration")
	flag.Parse()

	var data []byte
	var err error
	if *file != "" {
		data, err = ioutil.ReadFile(*file)
	} else {
		data, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fatal("reading input: ", err)
	}
	var in Instance
	if err := json.Unmarshal(data, &in); err != nil {
		fatal("parsing JSON: ", err)
	}

	res := Solve(&in)
	if !res.Feasible {
		fmt.Println(res.Reason)
		if *verify {
			printVerify(&in, res)
		}
		os.Exit(1)
	}

	m, errStr := buildModel(&in)
	if errStr != "" {
		fmt.Fprintln(os.Stderr, "invalid input:", errStr)
		os.Exit(2)
	}
	fmt.Printf("makespan: %d\n", res.Makespan)
	fmt.Println("starts:")
	for i, id := range m.ids {
		fmt.Printf("  %-8s start=%d  duration=%d  finish=%d\n",
			id, res.Starts[i], m.dur[i], res.Starts[i]+m.dur[i])
	}
	printEvidence(m, res)

	if *verify {
		printVerify(&in, res)
	}
}

// printEvidence renders the per-slot resource profile proving feasibility:
// which jobs occupy each slot and how much of each resource is consumed.
func printEvidence(m *model, res Result) {
	fmt.Println("resource evidence (per time slot):")
	header := "  time  jobs"
	for _, rn := range m.rnames {
		header += fmt.Sprintf("  %-8s", rn)
	}
	fmt.Println(header)
	for t := 0; t < res.Makespan; t++ {
		var running []string
		used := make([]int, m.r)
		for u := 0; u < m.n; u++ {
			if res.Starts[u] <= t && t < res.Starts[u]+m.dur[u] {
				running = append(running, m.ids[u])
				for ri := 0; ri < m.r; ri++ {
					used[ri] += m.dem[u][ri]
				}
			}
		}
		jobs := strings.Join(running, ",")
		if jobs == "" {
			jobs = "-"
		}
		line := fmt.Sprintf("  %4d  %-12s", t, jobs)
		for ri := 0; ri < m.r; ri++ {
			line += fmt.Sprintf("  %d/%-6d", used[ri], m.cap[ri])
		}
		fmt.Println(line)
	}
}

func printVerify(in *Instance, res Result) {
	brute := BruteSolve(in)
	switch {
	case brute.Feasible != res.Feasible:
		fmt.Printf("verify: MISMATCH solver feasible=%v brute feasible=%v\n",
			res.Feasible, brute.Feasible)
		os.Exit(2)
	case !brute.Feasible:
		fmt.Println("verify: OK (both infeasible)")
	case brute.Makespan != res.Makespan || !equalInts(brute.Starts, res.Starts):
		fmt.Printf("verify: MISMATCH solver=(%d %v) brute=(%d %v)\n",
			res.Makespan, res.Starts, brute.Makespan, brute.Starts)
		os.Exit(2)
	default:
		fmt.Printf("verify: OK (exhaustive enumeration agrees: makespan=%d starts=%v)\n",
			res.Makespan, res.Starts)
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

func fatal(prefix string, err error) {
	fmt.Fprintln(os.Stderr, prefix, err)
	os.Exit(2)
}
