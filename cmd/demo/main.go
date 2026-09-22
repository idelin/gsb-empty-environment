// Command demo runs a small DAG to show scheduling behavior:
//
//	root ──► fetch ──► validate ──► report
//	  └────► analytics (independent branch)
//
// "validate" fails on demand, so "report" becomes Blocked while the
// independent "analytics" branch still completes.
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"dagscheduler/dagscheduler"
)

func task(name string, d time.Duration, fail bool) dagscheduler.TaskFunc {
	return func(context.Context) error {
		fmt.Printf("  -> start  %s\n", name)
		time.Sleep(d)
		if fail {
			fmt.Printf("  !! fail   %s\n", name)
			return errors.New(name + " failed")
		}
		fmt.Printf("  <- done   %s\n", name)
		return nil
	}
}

func main() {
	s := dagscheduler.New()

	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}

	must(s.Add("root", task("root", 10*time.Millisecond, false)))
	must(s.Add("fetch", task("fetch", 20*time.Millisecond, false), "root"))
	must(s.Add("validate", task("validate", 20*time.Millisecond, true), "fetch"))
	must(s.Add("report", task("report", 10*time.Millisecond, false), "validate"))
	must(s.Add("analytics", task("analytics", 30*time.Millisecond, false), "root"))

	// Restricted dynamic edge: added before Run, so it is always accepted.
	must(s.AddDependency("analytics", "fetch"))

	fmt.Println("running DAG (validate is injected to fail)...")
	results, err := s.Run(context.Background())
	if err != nil {
		panic(err)
	}

	fmt.Println("\nresults:")
	for _, r := range results {
		line := fmt.Sprintf("  %-10s %s", r.Name, r.Status)
		if r.Err != nil {
			line += "  (" + r.Err.Error() + ")"
		}
		fmt.Println(line)
	}
}
