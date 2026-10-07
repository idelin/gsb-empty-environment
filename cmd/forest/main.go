// Command forest reads a complete operation sequence from stdin and prints
// one result line per operation, in order. Input format, one op per line:
//
//	link u v
//	cut u v
//	conn u v
//
// Blank lines and lines starting with '#' are ignored.
package main

import (
	"bufio"
	"fmt"
	"os"

	"forest"
)

func main() {
	var ops []forest.Op
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		var kind string
		var u, v int
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		if _, err := fmt.Sscanf(line, "%s %d %d", &kind, &u, &v); err != nil {
			fmt.Fprintf(os.Stderr, "line %d: %v\n", lineNo, err)
			os.Exit(1)
		}
		switch kind {
		case "link":
			ops = append(ops, forest.LinkOp(u, v))
		case "cut":
			ops = append(ops, forest.CutOp(u, v))
		case "conn":
			ops = append(ops, forest.ConnOp(u, v))
		default:
			fmt.Fprintf(os.Stderr, "line %d: unknown op %q\n", lineNo, kind)
			os.Exit(1)
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for i, r := range forest.Process(forest.New(), ops) {
		fmt.Printf("%d: %s\n", i+1, r)
	}
}
