package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
)

// inputJSON is the tool's input document.
type inputJSON struct {
	Costs  costsJSON `json:"costs"`
	Source *Node     `json:"source"`
	Target *Node     `json:"target"`
}

type mapEntry struct {
	Source      int    `json:"source"`
	SourceLabel string `json:"sourceLabel"`
	Target      int    `json:"target"`
	TargetLabel string `json:"targetLabel"`
	Renamed     bool   `json:"renamed"`
}

type idLabel struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}

type report struct {
	Distance int        `json:"distance"`
	Mapping  []mapEntry `json:"mapping"`
	Deleted  []idLabel  `json:"deleted"`
	Inserted []idLabel  `json:"inserted"`
	Ops      []Op       `json:"ops"`
	Checks   []string   `json:"checks"`
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("treeedit", flag.ContinueOnError)
	inPath := fs.String("i", "", "input JSON file (default: stdin)")
	asJSON := fs.Bool("json", false, "emit the report as JSON")
	brute := fs.String("brute", "auto", "exhaustive re-computation on short trees: auto|always|never")
	maxStates := fs.Int("max-states", 300000, "state cap for the exhaustive check")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var data []byte
	var err error
	if *inPath == "" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(*inPath)
	}
	if err != nil {
		return err
	}
	var in inputJSON
	if err := json.Unmarshal(data, &in); err != nil {
		return fmt.Errorf("invalid input JSON: %w", err)
	}
	if in.Source == nil || in.Target == nil {
		return fmt.Errorf("both \"source\" and \"target\" trees are required")
	}
	costs, err := parseCosts(in.Costs)
	if err != nil {
		return err
	}

	dist, ops, m, err := solveAll(in.Source, in.Target, costs)
	if err != nil {
		return err
	}
	t1 := indexTree(in.Source)
	t2 := indexTree(in.Target)

	rep := &report{Distance: dist, Ops: ops}
	pairs := append([][2]int{}, m.pairs...)
	sort.Slice(pairs, func(a, b int) bool { return pairs[a][0] < pairs[b][0] })
	for _, p := range pairs {
		rep.Mapping = append(rep.Mapping, mapEntry{
			Source: p[0], SourceLabel: t1.labels[p[0]],
			Target: p[1], TargetLabel: t2.labels[p[1]],
			Renamed: t1.labels[p[0]] != t2.labels[p[1]],
		})
	}
	del := append([]int{}, m.del...)
	sort.Ints(del)
	for _, d := range del {
		rep.Deleted = append(rep.Deleted, idLabel{ID: d, Label: t1.labels[d]})
	}
	ins := append([]int{}, m.ins...)
	sort.Ints(ins)
	for _, in2 := range ins {
		rep.Inserted = append(rep.Inserted, idLabel{ID: in2, Label: t2.labels[in2]})
	}

	// Independent verification: a separate applier applies the script to a
	// fresh copy of the source, recomputes every cost from the cost table
	// (charging single-node deletes, never whole subtrees) and requires the
	// result to equal the target and the total to equal the distance.
	if err := verifyScript(in.Source, in.Target, ops, dist, costs); err != nil {
		return fmt.Errorf("verification failed: %w", err)
	}
	rep.Checks = append(rep.Checks, "applier: script transforms source into target; recomputed cost matches distance")

	n1, n2 := Size(in.Source), Size(in.Target)
	runBrute := *brute == "always" || (*brute == "auto" && n1+n2 <= 8)
	switch *brute {
	case "auto", "always", "never":
	default:
		return fmt.Errorf("invalid -brute value %q (want auto|always|never)", *brute)
	}
	if runBrute {
		bd, ok := bruteForceDistance(in.Source, in.Target, costs, *maxStates)
		switch {
		case !ok:
			rep.Checks = append(rep.Checks, "brute force: inconclusive (state cap exceeded)")
		case bd != dist:
			return fmt.Errorf("verification failed: exhaustive search gives %d, dynamic program gives %d", bd, dist)
		default:
			rep.Checks = append(rep.Checks, fmt.Sprintf("brute force: exhaustive search confirms distance %d", bd))
		}
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	printReport(stdout, rep)
	return nil
}

func printReport(w io.Writer, rep *report) {
	fmt.Fprintf(w, "distance: %d\n", rep.Distance)
	fmt.Fprintln(w, "mapping (postorder ids):")
	for _, e := range rep.Mapping {
		note := ""
		if e.Renamed {
			note = "  (rename)"
		}
		fmt.Fprintf(w, "  #%d %q -> #%d %q%s\n", e.Source, e.SourceLabel, e.Target, e.TargetLabel, note)
	}
	for _, d := range rep.Deleted {
		fmt.Fprintf(w, "  #%d %q deleted (children lift into its place)\n", d.ID, d.Label)
	}
	for _, in := range rep.Inserted {
		fmt.Fprintf(w, "  #%d %q inserted\n", in.ID, in.Label)
	}
	fmt.Fprintln(w, "edit script:")
	for i, op := range rep.Ops {
		fmt.Fprintf(w, "  %d. %s\n", i+1, describeOp(op))
	}
	fmt.Fprintln(w, "checks:")
	for _, c := range rep.Checks {
		fmt.Fprintf(w, "  ok: %s\n", c)
	}
}

func describeOp(op Op) string {
	switch op.Kind {
	case "rename":
		return fmt.Sprintf("rename #%d %q -> %q (cost %d)", op.UID, op.From, op.To, op.Cost)
	case "delete":
		return fmt.Sprintf("delete #%d %q, lift its children (cost %d)", op.UID, op.Label, op.Cost)
	case "insert":
		return fmt.Sprintf("insert %q as #%d under #%d at index %d adopting %d following sibling(s) (cost %d)",
			op.Label, op.UID, op.ParentUID, op.Index, op.Adopt, op.Cost)
	}
	return fmt.Sprintf("%+v", op)
}
