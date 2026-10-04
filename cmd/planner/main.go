// Command planner reads a redemption planning problem as JSON and prints
// the optimal plan.
//
// Usage:
//
//	planner [-json] [input.json]
//
// With no file argument the input is read from stdin.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"pointsplanner/planner"
)

func main() {
	asJSON := flag.Bool("json", false, "print the plan as JSON")
	flag.Parse()

	var r io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		r = f
	}
	var in planner.Input
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		fatal(err)
	}
	s, err := planner.NewSolver(in)
	if err != nil {
		fatal(err)
	}
	plan, err := s.Solve()
	if err != nil {
		fatal(err)
	}
	if *asJSON {
		out, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			fatal(err)
		}
		fmt.Println(string(out))
		return
	}
	printText(plan)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "planner:", err)
	os.Exit(1)
}

func printText(p *planner.Plan) {
	for _, pp := range p.Periods {
		fmt.Printf("Period %d\n", pp.Period)
		for _, r := range pp.Redemptions {
			fmt.Printf("  redeem   %-14s x%d  cost=%d value=%d cashback=%d\n",
				r.RewardID, r.Quantity, r.Cost, r.Value, r.Cashback)
		}
		for _, d := range pp.Deductions {
			fmt.Printf("  deduct   %-14s -%d\n", d.BatchID, d.Amount)
		}
		for _, e := range pp.Expired {
			fmt.Printf("  expire   %-14s -%d\n", e.BatchID, e.Amount)
		}
		if pp.CashbackEarned > 0 {
			fmt.Printf("  cashback earned: %d (usable from period %d)\n", pp.CashbackEarned, pp.Period+1)
		}
		if len(pp.Redemptions) == 0 && len(pp.Expired) == 0 {
			fmt.Println("  (no activity)")
		}
	}
	fmt.Printf("Total: value=%d cost=%d cashback=%d expired=%d\n",
		p.TotalValue, p.TotalCost, p.TotalCashback, p.TotalExpired)
}
