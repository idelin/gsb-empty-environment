package main

import (
	"bufio"
	"fmt"
	"os"
)

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "error: "+msg)
	os.Exit(1)
}

// Input format (stdin):
//   n m
//   id u v w      (m lines; vertices are 0..n-1, ids unique, w integer)
//
// Output (stdout):
//   components <k>
//   total_weight <W>
//   edge <id> weight <w> -> replacement <id> weight <w>
//   edge <id> weight <w> -> disconnected
func main() {
	in := bufio.NewReader(os.Stdin)
	var n, m int
	if _, err := fmt.Fscan(in, &n, &m); err != nil {
		fatal("expected first line: n m")
	}
	if n < 1 || m < 0 {
		fatal("need n >= 1 and m >= 0")
	}
	edges := make([]Edge, 0, m)
	seen := make(map[int]bool)
	for i := 0; i < m; i++ {
		var id, u, v, w int
		if _, err := fmt.Fscan(in, &id, &u, &v, &w); err != nil {
			fatal(fmt.Sprintf("edge line %d: expected: id u v w", i+1))
		}
		if seen[id] {
			fatal(fmt.Sprintf("duplicate edge id %d", id))
		}
		seen[id] = true
		if u < 0 || u >= n || v < 0 || v >= n {
			fatal(fmt.Sprintf("edge %d: endpoint out of range [0,%d)", id, n))
		}
		edges = append(edges, Edge{ID: id, U: u, V: v, W: w})
	}

	res := Analyze(n, edges)

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	fmt.Fprintf(out, "components %d\n", res.Components)
	fmt.Fprintf(out, "total_weight %d\n", res.TotalWeight)
	for _, e := range res.TreeEdges {
		rep := res.Replacement[e.ID]
		if rep == nil {
			fmt.Fprintf(out, "edge %d weight %d -> disconnected\n", e.ID, e.W)
		} else {
			fmt.Fprintf(out, "edge %d weight %d -> replacement %d weight %d\n",
				e.ID, e.W, rep.ID, rep.W)
		}
	}
}
