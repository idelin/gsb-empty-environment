// Command stage2 solves the two-stage observation-based planning problem.
// Usage: stage2 [-verify] [instance.json]   (stdin when no file is given)
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"expsched/sched"
)

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "stage2:", err)
	os.Exit(1)
}

func main() {
	verify := flag.Bool("verify", false, "replay every scenario path and cross-check against naive enumeration")
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
	var inst sched.TwoStageInstance
	if err := json.NewDecoder(in).Decode(&inst); err != nil {
		fatal(err)
	}

	st := sched.SolveTwoStage(&inst)
	out, _ := json.MarshalIndent(st, "", "  ")
	fmt.Println(string(out))

	if *verify {
		if err := sched.VerifyStrategy(&inst, st); err != nil {
			fatal(err)
		}
		brute := sched.BruteTwoStage(&inst)
		if brute != st.Worst {
			fatal(fmt.Errorf("mismatch: solver worst=%d brute worst=%d", st.Worst, brute))
		}
		fmt.Fprintf(os.Stderr, "verification ok: per-path replay causal and legal, worst=%d matches enumeration (clairvoyant bound %d)\n",
			st.Worst, sched.ClairvoyantBound(&inst))
	}
}
