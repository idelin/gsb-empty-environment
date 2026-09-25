package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  storetool gen    -dir D [-segments N] [-keys K]   create a demo store
  storetool audit  -dir D [-plan-out plan.json]     read-only consistency audit
  storetool apply  -dir D -plan plan.json           execute a repair plan (resumable)

fault injection: STORETOOL_FAILPOINT="<actionID>:<phase>" crashes apply at that
point; phases: before, pre-rename, post-rename, pre-journal.`)
}

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	switch args[0] {
	case "gen":
		fs := flag.NewFlagSet("gen", flag.ContinueOnError)
		dir := fs.String("dir", "", "store directory")
		segs := fs.Int("segments", 3, "number of segments")
		keys := fs.Int("keys", 16, "keys per segment")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if *dir == "" {
			fmt.Fprintln(os.Stderr, "gen: -dir required")
			return 2
		}
		if err := Gen(*dir, *segs, *keys); err != nil {
			fmt.Fprintln(os.Stderr, "gen:", err)
			return 2
		}
		fmt.Printf("created store in %s (%d segments x %d keys)\n", *dir, *segs, *keys)
		return 0
	case "audit":
		fs := flag.NewFlagSet("audit", flag.ContinueOnError)
		dir := fs.String("dir", "", "store directory")
		planOut := fs.String("plan-out", "", "write repair plan JSON to this path")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if *dir == "" {
			fmt.Fprintln(os.Stderr, "audit: -dir required")
			return 2
		}
		if *planOut != "" {
			absDir, err1 := filepath.Abs(*dir)
			absOut, err2 := filepath.Abs(*planOut)
			if err1 == nil && err2 == nil &&
				(absOut == absDir || strings.HasPrefix(absOut, absDir+string(os.PathSeparator))) {
				fmt.Fprintln(os.Stderr, "audit: -plan-out must be outside the store directory (audit is read-only)")
				return 2
			}
		}
		rep, err := Audit(*dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "audit:", err)
			return 2
		}
		fmt.Printf("state summary: %s\n", rep.StateSummary)
		if rep.Clean() {
			fmt.Println("consistent: manifest, segments and index agree")
		} else {
			fmt.Printf("%d issue(s):\n", len(rep.Issues))
			for _, is := range rep.Issues {
				fmt.Printf("  [%-26s] %s %s\n", is.Code, is.Segment, is.Detail)
			}
			fmt.Printf("%d planned action(s):\n", len(rep.Plan.Actions))
			for _, a := range rep.Plan.Actions {
				fmt.Printf("  %s: %s %s (%s)\n", a.ID, a.Type, a.Segment, a.Reason)
			}
		}
		planData, err := json.MarshalIndent(rep.Plan, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "audit:", err)
			return 2
		}
		if *planOut != "" {
			if err := os.WriteFile(*planOut, planData, 0o644); err != nil {
				fmt.Fprintln(os.Stderr, "audit:", err)
				return 2
			}
			fmt.Printf("plan written to %s\n", *planOut)
		} else {
			fmt.Println(string(planData))
		}
		if rep.Clean() {
			return 0
		}
		return 1
	case "apply":
		fs := flag.NewFlagSet("apply", flag.ContinueOnError)
		dir := fs.String("dir", "", "store directory")
		planPath := fs.String("plan", "", "repair plan JSON produced by audit")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if *dir == "" || *planPath == "" {
			fmt.Fprintln(os.Stderr, "apply: -dir and -plan required")
			return 2
		}
		err := Apply(*dir, *planPath, os.Stdout)
		if errors.Is(err, errUnrecoverable) {
			fmt.Fprintln(os.Stderr, "apply:", err)
			return 4
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "apply:", err)
			return 2
		}
		return 0
	}
	usage()
	return 2
}
