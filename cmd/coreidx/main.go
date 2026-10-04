// Command coreidx builds and maintains a core-number index of an undirected
// simple graph.
//
// Usage:
//
//	coreidx build  -graph FILE
//	coreidx update -graph FILE -ops FILE
//
// Graph file format (one entry per line, '#' starts a comment):
//
//	N <id>      declare a node (may stay isolated)
//	E <u> <v>   declare an edge (endpoints are created implicitly)
//
// Ops file format:
//
//	A <u> <v>   add edge
//	D <u> <v>   delete edge
//	N <id>      add isolated node
//
// "build" prints, deterministically:
//
//	core <id> <k>                      one line per node, ascending id
//	peel <seq> <id> <deg> <core>       one line per peeling step, in order
//
// "update" applies the whole ops file as one atomic batch and prints one
// line per affected node, ascending id:
//
//	changed <id> <old-core> <new-core>
//
// If the batch fails (e.g. a self-loop) it is rolled back, an error is
// printed to stderr and the exit code is 1.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"coreidx/coreidx"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "build":
		err = cmdBuild(os.Args[2:])
	case "update":
		err = cmdUpdate(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "coreidx:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  coreidx build  -graph FILE
  coreidx update -graph FILE -ops FILE`)
}

func cmdBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	graphPath := fs.String("graph", "", "graph file (use - for stdin)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	g, err := readGraph(*graphPath)
	if err != nil {
		return err
	}
	res := g.Build()
	for _, id := range g.Nodes() {
		c, _ := g.Core(id)
		fmt.Printf("core %d %d\n", id, c)
	}
	for i, s := range res.Steps {
		fmt.Printf("peel %d %d %d %d\n", i, s.Node, s.Degree, s.Core)
	}
	return nil
}

func cmdUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	graphPath := fs.String("graph", "", "graph file (use - for stdin)")
	opsPath := fs.String("ops", "", "ops file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	g, err := readGraph(*graphPath)
	if err != nil {
		return err
	}
	ops, err := readOps(*opsPath)
	if err != nil {
		return err
	}
	g.Build()
	res, err := g.ApplyBatch(ops)
	if err != nil {
		return fmt.Errorf("batch rejected and rolled back: %w", err)
	}
	for _, c := range res.Changes {
		fmt.Printf("changed %d %d %d\n", c.Node, c.Old, c.New)
	}
	return nil
}

func openInput(path string) (io.ReadCloser, error) {
	if path == "-" {
		return io.NopCloser(os.Stdin), nil
	}
	return os.Open(path)
}

// nextLine returns the next non-empty, non-comment line, tokenized.
func nextLine(sc *bufio.Scanner) ([]string, bool) {
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) > 0 {
			return fields, true
		}
	}
	return nil, false
}

func parseID(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid node id %q", s)
	}
	return id, nil
}

func readGraph(path string) (*coreidx.Graph, error) {
	f, err := openInput(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	g := coreidx.New()
	sc := bufio.NewScanner(f)
	lineNo := 0
	for {
		fields, ok := nextLine(sc)
		if !ok {
			break
		}
		lineNo++
		fail := func(format string, a ...interface{}) (*coreidx.Graph, error) {
			return nil, fmt.Errorf("graph line %d: %s", lineNo, fmt.Sprintf(format, a...))
		}
		switch fields[0] {
		case "N":
			if len(fields) != 2 {
				return fail("N expects 1 argument")
			}
			id, err := parseID(fields[1])
			if err != nil {
				return fail("%v", err)
			}
			g.AddNode(id)
		case "E":
			if len(fields) != 3 {
				return fail("E expects 2 arguments")
			}
			u, err := parseID(fields[1])
			if err != nil {
				return fail("%v", err)
			}
			v, err := parseID(fields[2])
			if err != nil {
				return fail("%v", err)
			}
			if err := g.AddEdge(u, v); err != nil {
				return fail("%v", err)
			}
		default:
			return fail("unknown directive %q", fields[0])
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return g, nil
}

func readOps(path string) ([]coreidx.Op, error) {
	f, err := openInput(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var ops []coreidx.Op
	sc := bufio.NewScanner(f)
	lineNo := 0
	for {
		fields, ok := nextLine(sc)
		if !ok {
			break
		}
		lineNo++
		fail := func(format string, a ...interface{}) ([]coreidx.Op, error) {
			return nil, fmt.Errorf("ops line %d: %s", lineNo, fmt.Sprintf(format, a...))
		}
		switch fields[0] {
		case "N":
			if len(fields) != 2 {
				return fail("N expects 1 argument")
			}
			id, err := parseID(fields[1])
			if err != nil {
				return fail("%v", err)
			}
			ops = append(ops, coreidx.AddNodeOp(id))
		case "A", "D":
			if len(fields) != 3 {
				return fail("%s expects 2 arguments", fields[0])
			}
			u, err := parseID(fields[1])
			if err != nil {
				return fail("%v", err)
			}
			v, err := parseID(fields[2])
			if err != nil {
				return fail("%v", err)
			}
			if fields[0] == "A" {
				ops = append(ops, coreidx.AddEdgeOp(u, v))
			} else {
				ops = append(ops, coreidx.DeleteEdgeOp(u, v))
			}
		default:
			return fail("unknown op %q", fields[0])
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return ops, nil
}
