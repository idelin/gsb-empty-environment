package main

import (
	"bufio"
	"fmt"
	"os"
)

// Input format (stdin):
//   n m
//   id u v w      (m lines; vertices are 0..n-1, ids unique, w integer)
//
// Output (stdout):
//   total <weight>
//   edge <treeEdgeID> -> <replacementID | DISCONNECTED>   (sorted by tree edge ID)
func main() {
	in := bufio.NewReader(os.Stdin)
	var n, m int
	if _, err := fmt.Fscan(in, &n, &m); err != nil {
		fmt.Fprintln(os.Stderr, "error: cannot read 'n m':", err)
		os.Exit(1)
	}
	if n < 0 || m < 0 {
		fmt.Fprintln(os.Stderr, "error: n and m must be non-negative")
		os.Exit(1)
	}
	edges := make([]Edge, 0, m)
	seen := make(map[int]bool, m)
	for i := 0; i < m; i++ {
		var e Edge
		if _, err := fmt.Fscan(in, &e.ID, &e.U, &e.V, &e.W); err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot read edge #%d: %v\n", i, err)
			os.Exit(1)
		}
		if e.U < 0 || e.U >= n || e.V < 0 || e.V >= n {
			fmt.Fprintf(os.Stderr, "error: edge %d has vertex out of range [0,%d)\n", e.ID, n)
			os.Exit(1)
		}
		if seen[e.ID] {
			fmt.Fprintf(os.Stderr, "error: duplicate edge id %d\n", e.ID)
			os.Exit(1)
		}
		seen[e.ID] = true
		edges = append(edges, e)
	}

	res := Analyze(n, edges)

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	fmt.Fprintf(out, "total %d\n", res.Total)
	for _, e := range res.TreeEdges {
		if r := res.Replacement[e.ID]; r != nil {
			fmt.Fprintf(out, "edge %d -> %d\n", e.ID, r.ID)
		} else {
			fmt.Fprintf(out, "edge %d -> DISCONNECTED\n", e.ID)
		}
	}
}
