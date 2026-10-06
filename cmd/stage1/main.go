// Command stage1 solves the single-stage experiment selection problem.
// Usage: stage1 [-verify] [instance.json]   (stdin when no file is given)
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"expsched/sched"
)

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "stage1:", err)
	os.Exit(1)
}

func main() {
	verify := flag.Bool("verify", false, "cross-check with exhaustive subset enumeration and timeline replay")
	flag.Parse()

	in := os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		in = f
	}
	var inst sched.Instance
	if err := json.NewDecoder(in).Decode(&inst); err != nil {
		fatal(err)
	}

	sol := sched.Solve(&inst)
	out, _ := json.MarshalIndent(sol, "", "  ")
	fmt.Println(string(out))

	if *verify {
		idx := make(map[string]int)
		for i, c := range inst.Candidates {
			idx[c.ID] = i
		}
		chosen := []int{}
		for _, id := range sol.IDs {
			i, ok := idx[id]
			if !ok {
				fatal(fmt.Errorf("solution references unknown id %q", id))
			}
			chosen = append(chosen, i)
		}
		if !sched.ReplayLegal(&inst, chosen) {
			fatal(fmt.Errorf("timeline replay rejects solution %v", sol.IDs))
		}
		brute := sched.BruteForce(&inst)
		if brute.Profit != sol.Profit || fmt.Sprint(brute.IDs) != fmt.Sprint(sol.IDs) {
			fatal(fmt.Errorf("mismatch: solver=%v/%v brute=%v/%v", sol.Profit, sol.IDs, brute.Profit, brute.IDs))
		}
		fmt.Fprintln(os.Stderr, "verification ok: solver == exhaustive enumeration, timeline replay legal")
	}
}
