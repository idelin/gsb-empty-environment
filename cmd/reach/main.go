// Command reach applies edge updates read from stdin and prints the
// reachability pairs that change after every update.
//
// Input, one command per line:
//
//	add U V   insert one copy of edge U->V
//	del U V   remove one copy of edge U->V
//
// Output, one line per command:
//
//	+ U V     pair (U, V) became reachable
//	- U V     pair (U, V) became unreachable
//	.         no pair changed
package main

import (
	"bufio"
	"fmt"
	"os"

	"dynreach/reach"
)

func main() {
	g := reach.New()
	sc := bufio.NewScanner(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	line := 0
	for sc.Scan() {
		line++
		var op string
		var u, v int
		if _, err := fmt.Sscanf(sc.Text(), "%s %d %d", &op, &u, &v); err != nil {
			fmt.Fprintf(os.Stderr, "line %d: %v\n", line, err)
			continue
		}
		var changes []reach.Change
		switch op {
		case "add":
			changes = g.AddEdge(u, v)
		case "del":
			changes = g.RemoveEdge(u, v)
		default:
			fmt.Fprintf(os.Stderr, "line %d: unknown op %q\n", line, op)
			continue
		}
		if len(changes) == 0 {
			fmt.Fprintln(out, ".")
		}
		for _, c := range changes {
			sign := "-"
			if c.Added {
				sign = "+"
			}
			fmt.Fprintf(out, "%s %d %d\n", sign, c.From, c.To)
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
