// Command replay feeds spans from a JSON fixture into the assembler and
// prints every emitted event plus the final forest per trace.
//
// Usage: go run ./cmd/replay fixtures/out_of_order.json
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"traceasm"
)

type fixtureSpan struct {
	At           string `json:"at"`
	TraceID      string `json:"traceID"`
	SpanID       string `json:"spanID"`
	ParentSpanID string `json:"parentSpanID,omitempty"`
	Name         string `json:"name,omitempty"`
}

type fixture struct {
	OrphanTimeout string        `json:"orphanTimeout"`
	Spans         []fixtureSpan `json:"spans"`
}

func printTree(n *traceasm.TreeNode, indent string) {
	fmt.Printf("%s%s (%s) [%s]\n", indent, n.Span.SpanID, n.Span.Name, n.State)
	for _, c := range n.Children {
		printTree(c, indent+"  ")
	}
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: replay <fixture.json>")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var fx fixture
	if err := json.Unmarshal(data, &fx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	timeout, err := time.ParseDuration(fx.OrphanTimeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	asm := traceasm.New(timeout)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var events []traceasm.Event
	var last time.Time

	for _, fs := range fx.Spans {
		at, err := time.Parse(time.RFC3339, fs.At)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		last = at
		events = append(events, asm.Expire(at)...)
		events = append(events, asm.Ingest(traceasm.Span{
			TraceID:      fs.TraceID,
			SpanID:       fs.SpanID,
			ParentSpanID: fs.ParentSpanID,
			Name:         fs.Name,
			Start:        at,
		}, at)...)
	}
	// Final flush: let every pending orphan reach its deadline.
	events = append(events, asm.Expire(last.Add(2*timeout))...)

	fmt.Println("== events ==")
	for _, e := range events {
		line := fmt.Sprintf("[%s] %-20s trace=%s span=%s", e.At.Sub(base), e.Type, e.TraceID, e.SpanID)
		if e.ParentID != "" {
			line += " parent=" + e.ParentID
		}
		if e.Detail != "" {
			line += " -- " + e.Detail
		}
		fmt.Println(line)
	}

	fmt.Println("== forests ==")
	traces := asm.Traces()
	sort.Strings(traces)
	for _, id := range traces {
		f := asm.Tree(id)
		fmt.Printf("trace %s:\n", f.TraceID)
		for _, r := range f.Roots {
			printTree(r, "  ")
		}
		for _, u := range f.Unattached {
			fmt.Printf("  (unattached, waiting-for=%s)\n", u.Span.ParentSpanID)
			printTree(u, "    ")
		}
	}
}
