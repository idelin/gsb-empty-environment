package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

// MapEntry is one pair of the source<->target node mapping. Ids are
// postorder ids (1-based) within each tree; paths are child-index paths
// from the respective root.
type MapEntry struct {
	Source     int   `json:"source"`
	Target     int   `json:"target"`
	SourcePath []int `json:"sourcePath"`
	TargetPath []int `json:"targetPath"`
	Rename     bool  `json:"rename"`
}

type applyCheck struct {
	OK    bool   `json:"ok"`
	Cost  int    `json:"cost"`
	Error string `json:"error,omitempty"`
}

type exhaustiveCheck struct {
	Status   string `json:"status"` // ok | mismatch | inconclusive | skipped
	Distance int    `json:"distance,omitempty"`
	States   int    `json:"states,omitempty"`
}

type output struct {
	Distance     int          `json:"distance"`
	Mapping      []MapEntry   `json:"mapping"`
	Operations   []Op         `json:"operations"`
	Verification verification `json:"verification"`
}

type verification struct {
	Apply      applyCheck      `json:"apply"`
	Exhaustive exhaustiveCheck `json:"exhaustive"`
}

func main() {
	var srcPath, tgtPath, costsPath string
	var maxExNodes, maxStates int
	flag.StringVar(&srcPath, "src", "", "source tree JSON file")
	flag.StringVar(&tgtPath, "tgt", "", "target tree JSON file")
	flag.StringVar(&costsPath, "costs", "", "cost table JSON file (default: unit costs)")
	flag.IntVar(&maxExNodes, "exhaustive-max-nodes", 12, "run exhaustive verification when n1+n2 <= this (0 disables)")
	flag.IntVar(&maxStates, "exhaustive-max-states", 300000, "state budget for exhaustive verification")
	flag.Parse()
	if srcPath == "" || tgtPath == "" {
		fmt.Fprintln(os.Stderr, "usage: treeedit -src tree1.json -tgt tree2.json [-costs costs.json]")
		os.Exit(2)
	}

	t1, err := readTree(srcPath)
	fatal(err)
	t2, err := readTree(tgtPath)
	fatal(err)

	costs := DefaultCosts()
	if costsPath != "" {
		data, err := os.ReadFile(costsPath)
		fatal(err)
		fatal(json.Unmarshal(data, costs))
	}
	fatal(costs.Validate())

	out, failed := run(t1, t2, costs, maxExNodes, maxStates)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	fatal(enc.Encode(out))
	if failed {
		os.Exit(1)
	}
}

func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
}

func readTree(path string) (*Tree, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseTree(data)
}

// run computes the distance, mapping and script, then verifies everything.
func run(t1, t2 *Tree, c *CostTable, maxExNodes, maxStates int) (*output, bool) {
	nc := normalizeCosts(c, t1, t2)
	dist, mapping := treeEditDistance(t1, t2, nc)

	ops, err := buildScript(t1, t2, mapping, nc)
	fatal(err)

	f1, f2 := flatten(t1), flatten(t2)
	out := &output{Distance: dist, Operations: ops}
	for _, pr := range mapping {
		s, t := pr[0], pr[1]
		out.Mapping = append(out.Mapping, MapEntry{
			Source:     s,
			Target:     t,
			SourcePath: f1.path(s),
			TargetPath: f2.path(t),
			Rename:     f1.labels[s] != f2.labels[t],
		})
	}
	sortMapping(out.Mapping)

	failed := false

	// Independent applier check: apply the script to the source and compare.
	resTree, cost, err := ApplyScript(t1, ops, c)
	ac := applyCheck{Cost: cost}
	switch {
	case err != nil:
		ac.Error = err.Error()
	case cost != dist:
		ac.Error = fmt.Sprintf("script cost %d != distance %d", cost, dist)
	case !Equal(resTree, t2):
		ac.Error = "script result != target tree"
	default:
		ac.OK = true
	}
	if !ac.OK {
		failed = true
	}
	out.Verification.Apply = ac

	// Exhaustive recomputation for short trees.
	n1, n2 := countNodes(t1), countNodes(t2)
	ec := exhaustiveCheck{Status: "skipped"}
	if maxExNodes > 0 && n1+n2 <= maxExNodes {
		ed, states, ok := ExhaustiveDistance(t1, t2, c, maxStates)
		ec.States = states
		switch {
		case !ok:
			ec.Status = "inconclusive"
		case ed != dist:
			ec.Status = "mismatch"
			ec.Distance = ed
			failed = true
		default:
			ec.Status = "ok"
			ec.Distance = ed
		}
	}
	out.Verification.Exhaustive = ec

	return out, failed
}

func sortMapping(m []MapEntry) {
	for i := 1; i < len(m); i++ {
		for j := i; j > 0 && m[j].Source < m[j-1].Source; j-- {
			m[j], m[j-1] = m[j-1], m[j]
		}
	}
}
