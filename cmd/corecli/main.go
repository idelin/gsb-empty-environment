// Command corecli builds and updates an incremental core-number index.
//
// Usage:
//
//	corecli build  -edges FILE [-state FILE]
//	corecli update -state FILE -ops FILE [-out FILE]
//	corecli check  -state FILE
//
// Edge file format (one per line, '#' starts a comment):
//
//	<u> <v>   an edge
//	N <u>     an isolated node
//
// Ops file format:
//
//	A <u> <v> insert edge
//	D <u> <v> delete edge
//	N <u>     ensure isolated node exists
//
// All ops in one file form a single atomic batch: if any op is invalid
// (self-loop), nothing is applied and the state file is left untouched.
//
// build prints the core value of every node, the deterministic peeling
// order and the residual degrees for verification. update prints the
// nodes whose core value changed. check recomputes a full decomposition
// from the stored graph as a reference for the incremental state.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"

	"coreindex"
)

type stateFile struct {
	Nodes []int64    `json:"nodes"`
	Edges [][2]int64 `json:"edges"`
}

type buildOut struct {
	Core   map[int64]int `json:"core"`
	Order  []int64       `json:"order"`
	RemDeg []int         `json:"remdeg"`
}

type updateOut struct {
	Changed map[int64]int `json:"changed"`
	Core    map[int64]int `json:"core"`
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) < 1 {
		usage()
		return 2
	}
	var err error
	switch args[0] {
	case "build":
		err = cmdBuild(args[1:])
	case "update":
		err = cmdUpdate(args[1:])
	case "check":
		err = cmdCheck(args[1:])
	default:
		usage()
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "corecli:", err)
		return 1
	}
	return 0
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: corecli <build|update|check> [flags]")
}

func cmdBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	edgesPath := fs.String("edges", "", "edge list file")
	statePath := fs.String("state", "", "write index state to this file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *edgesPath == "" {
		return fmt.Errorf("-edges is required")
	}
	edges, nodes, err := parseEdgeFile(*edgesPath)
	if err != nil {
		return err
	}
	ix := coreindex.New()
	for _, n := range nodes {
		ix.EnsureNode(n)
	}
	var ops []coreindex.EdgeOp
	for _, e := range edges {
		ops = append(ops, coreindex.AddOp(e[0], e[1]))
	}
	if _, err := ix.ApplyBatch(ops); err != nil {
		return err
	}
	d := ix.Build()
	if !d.Consistent() {
		return fmt.Errorf("internal error: inconsistent decomposition")
	}
	out := buildOut{Core: d.Core, Order: d.Order, RemDeg: d.RemDeg}
	if err := printJSON(out); err != nil {
		return err
	}
	if *statePath != "" {
		return saveState(*statePath, ix)
	}
	return nil
}

func cmdUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	statePath := fs.String("state", "", "index state file")
	opsPath := fs.String("ops", "", "ops batch file")
	outPath := fs.String("out", "", "write new state here (default: overwrite -state)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *statePath == "" || *opsPath == "" {
		return fmt.Errorf("-state and -ops are required")
	}
	ix, err := loadState(*statePath)
	if err != nil {
		return err
	}
	ops, ensure, err := parseOpsFile(*opsPath)
	if err != nil {
		return err
	}
	changed, err := ix.ApplyBatch(ops, ensure...)
	if err != nil {
		return err // state file untouched: batch rolled back
	}
	if err := printJSON(updateOut{Changed: changed, Core: ix.Cores()}); err != nil {
		return err
	}
	out := *outPath
	if out == "" {
		out = *statePath
	}
	return saveState(out, ix)
}

func cmdCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	statePath := fs.String("state", "", "index state file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *statePath == "" {
		return fmt.Errorf("-state is required")
	}
	ix, err := loadState(*statePath)
	if err != nil {
		return err
	}
	d := ix.Decompose() // full decomposition as reference
	return printJSON(buildOut{Core: d.Core, Order: d.Order, RemDeg: d.RemDeg})
}

func loadState(path string) (*coreindex.Index, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sf stateFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, err
	}
	ix := coreindex.New()
	for _, n := range sf.Nodes {
		ix.EnsureNode(n)
	}
	var ops []coreindex.EdgeOp
	for _, e := range sf.Edges {
		ops = append(ops, coreindex.AddOp(e[0], e[1]))
	}
	if _, err := ix.ApplyBatch(ops); err != nil {
		return nil, err
	}
	ix.Build()
	return ix, nil
}

func saveState(path string, ix *coreindex.Index) error {
	sf := stateFile{Nodes: ix.Nodes(), Edges: ix.Edges()}
	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, data, 0644)
}

func printJSON(v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

// parseEdgeFile parses "u v" edge lines and "N u" node lines.
func parseEdgeFile(path string) (edges [][2]int64, nodes []int64, err error) {
	lines, err := readLines(path)
	if err != nil {
		return nil, nil, err
	}
	for i, line := range lines {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if f[0] == "N" || f[0] == "n" {
			if len(f) != 2 {
				return nil, nil, fmt.Errorf("%s:%d: bad node line", path, i+1)
			}
			id, err := atoi(f[1])
			if err != nil {
				return nil, nil, fmt.Errorf("%s:%d: %v", path, i+1, err)
			}
			nodes = append(nodes, id)
			continue
		}
		if len(f) != 2 {
			return nil, nil, fmt.Errorf("%s:%d: want \"u v\" or \"N u\"", path, i+1)
		}
		u, err := atoi(f[0])
		if err != nil {
			return nil, nil, fmt.Errorf("%s:%d: %v", path, i+1, err)
		}
		v, err := atoi(f[1])
		if err != nil {
			return nil, nil, fmt.Errorf("%s:%d: %v", path, i+1, err)
		}
		edges = append(edges, [2]int64{u, v})
	}
	return edges, nodes, nil
}

// parseOpsFile parses "A u v", "D u v" and "N u" lines.
func parseOpsFile(path string) (ops []coreindex.EdgeOp, ensure []int64, err error) {
	lines, err := readLines(path)
	if err != nil {
		return nil, nil, err
	}
	for i, line := range lines {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		bad := func() error { return fmt.Errorf("%s:%d: want \"A u v\", \"D u v\" or \"N u\"", path, i+1) }
		switch strings.ToUpper(f[0]) {
		case "N":
			if len(f) != 2 {
				return nil, nil, bad()
			}
			id, err := atoi(f[1])
			if err != nil {
				return nil, nil, fmt.Errorf("%s:%d: %v", path, i+1, err)
			}
			ensure = append(ensure, id)
		case "A", "D":
			if len(f) != 3 {
				return nil, nil, bad()
			}
			u, err := atoi(f[1])
			if err != nil {
				return nil, nil, fmt.Errorf("%s:%d: %v", path, i+1, err)
			}
			v, err := atoi(f[2])
			if err != nil {
				return nil, nil, fmt.Errorf("%s:%d: %v", path, i+1, err)
			}
			ops = append(ops, coreindex.EdgeOp{Add: f[0] == "A" || f[0] == "a", U: u, V: v})
		default:
			return nil, nil, bad()
		}
	}
	return ops, ensure, nil
}

func readLines(path string) ([]string, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		out = append(out, line)
	}
	return out, nil
}

func atoi(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}
