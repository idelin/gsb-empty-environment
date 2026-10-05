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

// Input format, one event per line (blank lines and '#' comments ignored):
//
//	<tick> add   <u> <v>
//	<tick> del   <u> <v>
//	<tick> query <u> <v>
//
// Events are applied in input order; ties on the same tick are ordered by
// their position in the file. A query observes exactly the state produced
// by all preceding events. Duplicate adds of the same edge are reference
// counted; deleting an edge whose count is zero is an error.
func main() {
	verify := flag.Bool("verify", true, "cross-check every answer by rebuilding the graph at each query time")
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

	events, err := parseEvents(r)
	if err != nil {
		fatal(err)
	}
	answers, err := solve(events)
	if err != nil {
		fatal(err)
	}
	if *verify {
		if err := verifyByRebuild(events, answers); err != nil {
			fatal(fmt.Errorf("verification failed: %w", err))
		}
	}
	for _, a := range answers {
		fmt.Println(formatAnswer(a))
	}
	if *verify {
		fmt.Fprintf(os.Stderr, "verification: OK (%d queries cross-checked against per-tick rebuilt graphs)\n", len(answers))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func parseEvents(r io.Reader) ([]event, error) {
	var events []event
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
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
		f := strings.Fields(line)
		if len(f) != 4 {
			return nil, fmt.Errorf("line %d: want 4 fields '<tick> <op> <u> <v>', got %q", lineNo, line)
		}
		tick, err := strconv.Atoi(f[0])
		if err != nil {
			return nil, fmt.Errorf("line %d: bad tick: %w", lineNo, err)
		}
		u, err := strconv.Atoi(f[2])
		if err != nil {
			return nil, fmt.Errorf("line %d: bad vertex: %w", lineNo, err)
		}
		v, err := strconv.Atoi(f[3])
		if err != nil {
			return nil, fmt.Errorf("line %d: bad vertex: %w", lineNo, err)
		}
		var op opKind
		switch strings.ToLower(f[1]) {
		case "add", "a", "+":
			op = opAdd
		case "del", "delete", "remove", "d", "-":
			op = opDel
		case "query", "q", "connected", "path":
			op = opQuery
		default:
			return nil, fmt.Errorf("line %d: unknown op %q (want add|del|query)", lineNo, f[1])
		}
		events = append(events, event{tick: tick, op: op, u: u, v: v})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func formatAnswer(a answer) string {
	head := fmt.Sprintf("tick %d query %d %d:", a.tick, a.u, a.v)
	if a.connected {
		return fmt.Sprintf("%s connected path=%s", head, joinPath(a.path))
	}
	return fmt.Sprintf("%s disconnected cut=%v | %v crossing_edges=0", head, a.comp, a.rest)
}

func joinPath(path []int) string {
	parts := make([]string, len(path))
	for i, v := range path {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, "-")
}
