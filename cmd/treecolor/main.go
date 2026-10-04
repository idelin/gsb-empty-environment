// Command treecolor reads a tree and an operation sequence from stdin and
// prints one answer per query, in input order (deterministic output).
//
// Input format:
//
//	n
//	u v            (n-1 edge lines, 0-based)
//	c0 c1 ... c(n-1)
//	q
//	op             (q lines: "C node color" or "Q u v")
package main

import (
	"bufio"
	"fmt"
	"os"

	"treecolor/treecolor"
)

func main() {
	in := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	var n int
	if _, err := fmt.Fscan(in, &n); err != nil {
		fmt.Fprintln(os.Stderr, "read n:", err)
		os.Exit(1)
	}
	edges := make([][2]int, 0, n-1)
	for i := 0; i < n-1; i++ {
		var u, v int
		fmt.Fscan(in, &u, &v)
		edges = append(edges, [2]int{u, v})
	}
	init := make([]int64, n)
	for i := range init {
		fmt.Fscan(in, &init[i])
	}
	var q int
	fmt.Fscan(in, &q)
	ops := make([]treecolor.Op, 0, q)
	for i := 0; i < q; i++ {
		var kind string
		fmt.Fscan(in, &kind)
		switch kind {
		case "C":
			var node int
			var color int64
			fmt.Fscan(in, &node, &color)
			ops = append(ops, treecolor.Op{Kind: treecolor.OpUpdate, Node: node, Color: color})
		case "Q":
			var u, v int
			fmt.Fscan(in, &u, &v)
			ops = append(ops, treecolor.Op{Kind: treecolor.OpQuery, U: u, V: v})
		default:
			fmt.Fprintln(os.Stderr, "unknown op:", kind)
			os.Exit(1)
		}
	}
	for _, ans := range treecolor.Solve(n, edges, init, ops, 0) {
		fmt.Fprintln(out, ans)
	}
}
