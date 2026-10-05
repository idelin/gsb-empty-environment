package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
)

type inputJSON struct {
	Nodes int     `json:"nodes"`
	Old   [][]int `json:"old"`
	New   [][]int `json:"new"`
}

func usage() {
	fmt.Fprintf(os.Stderr, `routemigrate - loop-free routing update planner

Reads old/new next-hop tables (JSON) and computes an update plan that never
creates a forwarding loop or blackhole in any intermediate state.

Input JSON:
  {
    "nodes": 4,
    "old": [[...], ...],   // old[i][d] = next hop of node i toward dest d
    "new": [[...], ...]    // -1 = no route; diagonal entries are ignored
  }

The plan minimizes the largest synchronized update group (primary) and the
number of groups (secondary). A plan with max group size 1 is a safe
node-by-node update order.

Usage:
  routemigrate -input plan.json [-v]
  cat plan.json | routemigrate -input -

Flags:
`)
	flag.PrintDefaults()
}

func main() {
	input := flag.String("input", "-", "input JSON file, or - for stdin")
	verbose := flag.Bool("v", false, "print full per-source forwarding paths as evidence")
	flag.Usage = usage
	flag.Parse()

	var raw []byte
	var err error
	if *input == "-" {
		raw, err = ioutil.ReadAll(os.Stdin)
	} else {
		raw, err = ioutil.ReadFile(*input)
	}
	if err != nil {
		fatal("reading input: %v", err)
	}
	var in inputJSON
	if err := json.Unmarshal(raw, &in); err != nil {
		fatal("parsing JSON: %v", err)
	}
	p, err := NewPlanner(Tables{N: in.Nodes, Old: in.Old, New: in.New})
	if err != nil {
		fatal("invalid tables: %v", err)
	}

	fmt.Printf("nodes: %d, nodes needing update: %s\n", p.n, listOrNone(p.diff))

	oldSafe, oldEv := p.Check(0)
	fmt.Printf("initial (all-old) state: %s\n", safeWord(oldSafe))
	if !oldSafe {
		fmt.Print(p.FormatEvidence(oldEv, *verbose))
	}
	newSafe, newEv := p.Check(p.fullMask())
	fmt.Printf("final (all-new) state:   %s\n", safeWord(newSafe))
	if !newSafe {
		fmt.Print(p.FormatEvidence(newEv, *verbose))
		fatal("the new configuration itself is unsafe; no update plan can converge to it")
	}
	if len(p.diff) == 0 {
		fmt.Println("old and new tables are identical; nothing to do")
		return
	}

	plan, ok := p.FindPlan()
	if !ok {
		fatal("no safe update plan exists")
	}
	if plan.MaxSize == 1 {
		fmt.Printf("\nSAFE PER-NODE UPDATE ORDER (%d steps, no synchronization needed):\n", len(plan.Groups))
	} else {
		fmt.Printf("\nNO safe per-node order exists. Blocking evidence (each single first update):\n")
		p.printBlockingEvidence(*verbose)
		fmt.Printf("MINIMUM SYNCHRONIZATION PLAN: %d groups, largest group = %d node(s)\n",
			len(plan.Groups), plan.MaxSize)
	}
	for gi, g := range plan.Groups {
		fmt.Printf("  step %d: update {%s}\n", gi+1, listOrNone(g))
	}
	fmt.Println()
	p.printEvidence(plan, *verbose)
}

func (p *Planner) printEvidence(plan Plan, verbose bool) {
	_, ev0 := p.Check(0)
	fmt.Println("evidence, step 0 (initial state):")
	fmt.Print(p.FormatEvidence(ev0, verbose))
	for i, mask := range plan.States {
		safe, ev := p.Check(mask)
		fmt.Printf("evidence, step %d: after update {%s} => %s\n",
			i+1, listOrNone(plan.Groups[i]), safeWord(safe))
		fmt.Print(p.FormatEvidence(ev, verbose))
	}
}

// printBlockingEvidence shows, for every node that could be updated first,
// the loop/blackhole that update would create (or that it is safe).
func (p *Planner) printBlockingEvidence(verbose bool) {
	for _, v := range p.diff {
		safe, ev := p.Check(uint32(1) << uint(v))
		fmt.Printf("  update n%d first => %s\n", v, safeWord(safe))
		if !safe {
			fmt.Print(indent(p.FormatEvidence(ev, verbose)))
		}
	}
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n") + "\n"
}

func listOrNone(vs []int) string {
	if len(vs) == 0 {
		return "(none)"
	}
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = fmt.Sprintf("n%d", v)
	}
	return strings.Join(parts, ",")
}

func safeWord(ok bool) string {
	if ok {
		return "SAFE (loop-free, all destinations reachable)"
	}
	return "UNSAFE"
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
