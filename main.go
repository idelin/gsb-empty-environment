package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func main() {
	verify := flag.Bool("verify", false, "cross-check every answer by rebuilding the graph at each query's own time")
	flag.Parse()

	var r io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		r = f
	}

	ops, err := parseOps(r)
	if err != nil {
		fatal(err)
	}
	answers, err := solve(ops)
	if err != nil {
		fatal(err)
	}
	if *verify {
		if err := verifyAgainstRebuild(ops, answers); err != nil {
			fatal(fmt.Errorf("verification failed: %w", err))
		}
		fmt.Fprintf(os.Stderr, "verification ok: %d quer(ies) cross-checked against per-time rebuilds\n", len(answers))
	}
	for _, a := range answers {
		fmt.Println(a.String())
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

// parseOps reads lines of the form:
//
//	<tick> <add|del|query> <u> <v>
//
// Blank lines and lines starting with '#' are ignored. Time is defined by
// file order (top to bottom); the tick is a label echoed in the output, and
// multiple operations may share one tick — earlier lines take effect first.
func parseOps(r io.Reader) ([]op, error) {
	var ops []op
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		s := strings.TrimSpace(scanner.Text())
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		fields := strings.Fields(s)
		if len(fields) != 4 {
			return nil, fmt.Errorf("line %d: expected 4 fields: <tick> <add|del|query> <u> <v>", lineNo)
		}
		tick, err := strconv.Atoi(fields[0])
		if err != nil {
			return nil, fmt.Errorf("line %d: bad tick %q: %v", lineNo, fields[0], err)
		}
		var kind opKind
		switch fields[1] {
		case "add", "+":
			kind = opAdd
		case "del", "-":
			kind = opDel
		case "query", "?":
			kind = opQuery
		default:
			return nil, fmt.Errorf("line %d: unknown operation %q (want add|del|query)", lineNo, fields[1])
		}
		u, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("line %d: bad vertex %q: %v", lineNo, fields[2], err)
		}
		v, err := strconv.Atoi(fields[3])
		if err != nil {
			return nil, fmt.Errorf("line %d: bad vertex %q: %v", lineNo, fields[3], err)
		}
		ops = append(ops, op{tick: tick, kind: kind, u: u, v: v, line: lineNo})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return ops, nil
}
