// Command treeplan plans group expansions for a collapsible tree viewer.
//
//	treeplan expand-set spec.json   # problem 1: cheapest expansion set
//	treeplan sequence  spec.json    # problem 2: cheapest action plan under budget
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"treeplan"
)

func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func loadSpec(path string) (*treeplan.Tree, *treeplan.Spec) {
	data, err := ioutil.ReadFile(path)
	fatal(err)
	var spec treeplan.Spec
	fatal(json.Unmarshal(data, &spec))
	tr, err := treeplan.BuildTree(&spec)
	fatal(err)
	return tr, &spec
}

func printJSON(v interface{}) {
	out, err := json.MarshalIndent(v, "", "  ")
	fatal(err)
	fmt.Println(string(out))
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: treeplan <expand-set|sequence> <spec.json>")
		os.Exit(2)
	}
	tr, spec := loadSpec(os.Args[2])
	switch os.Args[1] {
	case "expand-set":
		res, err := tr.SolveExpandSet()
		fatal(err)
		fatal(tr.VerifyExpandSet(res))
		if dp := tr.SolveExpandSetDP(); dp.Cost != res.Cost {
			fatal(fmt.Errorf("cross-check failed: enumeration %d, DP %d", res.Cost, dp.Cost))
		}
		printJSON(res)
	case "sequence":
		plan, err := tr.SolveSequence(spec.Budget)
		fatal(err)
		ledger, err := tr.Replay(plan.Actions, plan.Budget)
		fatal(err)
		printJSON(plan)
		fmt.Println("\nreference ledger:")
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "STEP\tACTION\tNAME\tCOST\tMEM\tPEAK\tREFS\tNOTE")
		for _, e := range ledger.Entries {
			fmt.Fprintf(w, "%d\t%s\t%s\t%d\t%d\t%d\t%s\t%s\n",
				e.Step, e.Action.Kind, e.Action.Name, e.Cost, e.Memory, e.Peak, formatRefs(e.Refs), e.Note)
		}
		w.Flush()
	default:
		fmt.Fprintln(os.Stderr, "unknown subcommand:", os.Args[1])
		os.Exit(2)
	}
}

func formatRefs(refs map[string]int) string {
	if len(refs) == 0 {
		return "-"
	}
	var parts []string
	for name, c := range refs {
		parts = append(parts, fmt.Sprintf("%s:%d", name, c))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}
