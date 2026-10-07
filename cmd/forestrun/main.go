// Command forestrun reads an offline operation sequence from stdin and
// prints one result line per operation, followed by the failure positions.
//
// Input format (one op per line, '#' starts a comment):
//
//	add u v
//	remove u v
//	query u v
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"forest/forest"
)

func main() {
	var ops []forest.Op
	sc := bufio.NewScanner(os.Stdin)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		var kind string
		var u, v int
		if _, err := fmt.Sscanf(line, "%s %d %d", &kind, &u, &v); err != nil {
			fmt.Fprintf(os.Stderr, "line %d: %v\n", lineNo, err)
			os.Exit(1)
		}
		var op forest.Op
		op.U, op.V = u, v
		switch kind {
		case "add":
			op.Kind = forest.OpAdd
		case "remove":
			op.Kind = forest.OpRemove
		case "query":
			op.Kind = forest.OpQuery
		default:
			fmt.Fprintf(os.Stderr, "line %d: unknown op %q\n", lineNo, kind)
			os.Exit(1)
		}
		ops = append(ops, op)
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	rep := forest.Run(ops)
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	for _, r := range rep.Results {
		fmt.Fprintln(w, r.String())
	}
	if len(rep.Failures) > 0 {
		pos := make([]string, len(rep.Failures))
		for i, f := range rep.Failures {
			pos[i] = fmt.Sprintf("%d", f.Pos)
		}
		fmt.Fprintf(w, "failures at positions: %s\n", strings.Join(pos, ", "))
	}
}
