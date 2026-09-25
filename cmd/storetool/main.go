// storetool audits manifest/segment/index consistency and applies
// repair plans. Fault injection for demos: set STORETOOL_FAULT to a
// comma list of points, e.g. STORETOOL_FAULT=after_rename.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"storetool/store"
)

func usage() {
	fmt.Fprintf(os.Stderr, `usage:
  storetool gen   -dir D                      create a deterministic sample store
  storetool audit -dir D -out plan.json       read-only audit, writes repair plan
  storetool apply -dir D -plan plan.json      verify summary, apply plan (resumable)

fault injection (apply): STORETOOL_FAULT=before_step,before_rename,after_rename,after_journal
`)
	os.Exit(2)
}

func flagValue(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, name+"=") {
			return a[len(name)+1:]
		}
	}
	return ""
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	args := os.Args[2:]
	dir := flagValue(args, "-dir")
	switch os.Args[1] {
	case "gen":
		must(store.Gen(dir))
	case "audit":
		out := flagValue(args, "-out")
		rep, plan, err := store.Audit(dir)
		must(err)
		repJSON, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(repJSON))
		if out != "" {
			planJSON, _ := json.MarshalIndent(plan, "", "  ")
			must(os.WriteFile(out, planJSON, 0o644))
			fmt.Fprintf(os.Stderr, "plan %s written to %s (repairable=%v, %d step(s))\n",
				plan.ID, out, plan.Repairable, len(plan.Steps))
		}
	case "apply":
		armFaults()
		planPath := flagValue(args, "-plan")
		data, err := os.ReadFile(planPath)
		must(err)
		var plan store.Plan
		must(json.Unmarshal(data, &plan))
		must(store.Apply(dir, &plan))
		fmt.Println("apply completed: store is consistent")
	default:
		usage()
	}
}

func armFaults() {
	spec := os.Getenv("STORETOOL_FAULT")
	if spec == "" {
		return
	}
	armed := map[string]bool{}
	for _, p := range strings.Split(spec, ",") {
		armed[strings.TrimSpace(p)] = true
	}
	store.FaultHook = func(point store.FaultPoint, stepID string) error {
		if armed[string(point)] {
			return &store.InjectedFault{Point: point, StepID: stepID}
		}
		return nil
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
