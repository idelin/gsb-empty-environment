// Command tracecp replays a synthesized telemetry fixture (JSON)
// and prints the critical-path analysis of the trace DAG.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"tracecp/internal/trace"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: tracecp <fixture.json>\n")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "read fixture: %v\n", err)
		os.Exit(1)
	}
	var t trace.Trace
	if err := json.Unmarshal(data, &t); err != nil {
		fmt.Fprintf(os.Stderr, "parse fixture: %v\n", err)
		os.Exit(1)
	}
	printReport(trace.Analyze(&t))
}

func printReport(r trace.Report) {
	fmt.Printf("trace: %s\n", r.TraceID)
	fmt.Printf("spans: %d\n", r.SpanCount)
	fmt.Printf("wall-clock: %dms\n", r.WallClock)
	fmt.Printf("total work: %dms\n", r.TotalWork)
	if r.CriticalPath != nil {
		fmt.Printf("critical path: %s (wall %dms, work %dms)\n",
			strings.Join(r.CriticalPath.Spans, " -> "),
			r.CriticalPath.WallClock, r.CriticalPath.Work)
	} else {
		fmt.Printf("critical path: unavailable (dependency cycle: %s)\n",
			strings.Join(r.Cycles, ", "))
	}
	if len(r.NegativeDurations) == 0 && len(r.CausalityViolations) == 0 {
		fmt.Println("anomalies: none")
		return
	}
	for _, id := range r.NegativeDurations {
		fmt.Printf("anomaly[negative-duration]: span %s\n", id)
	}
	for _, v := range r.CausalityViolations {
		fmt.Printf("anomaly[causality]: %s\n", v)
	}
}
