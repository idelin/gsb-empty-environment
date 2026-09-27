package main

import (
	"fmt"
	"os"
	"sort"
)

func main() {
	traces, replayErrs := Replay(SampleEvents())
	if len(replayErrs) > 0 {
		for _, err := range replayErrs {
			fmt.Fprintln(os.Stderr, "replay:", err)
		}
		os.Exit(1)
	}

	traceIDs := make([]string, 0, len(traces))
	for traceID := range traces {
		traceIDs = append(traceIDs, traceID)
	}
	sort.Strings(traceIDs)

	for _, traceID := range traceIDs {
		report := AnalyzeTrace(traceID, traces[traceID])
		printReport(report)
	}
}

func printReport(report Report) {
	fmt.Printf("trace=%s status=%s spans=%d valid=%d\n", report.TraceID, report.Status, report.SpanCount, report.ValidSpans)
	fmt.Printf("  timeline_wall=%s accumulated_work=%s\n", report.TimelineWall, report.TotalWork)
	if report.Status != statusCycle {
		fmt.Printf("  critical_wall=%s critical_work=%s async_wait=%s path=%v\n",
			report.Critical.Wall, report.Critical.Work, report.Critical.IdleWaiting, report.Critical.SpanIDs)
	} else {
		fmt.Println("  critical_path=none (dependency cycle; no pseudo-path is emitted)")
	}
	for _, problem := range report.Problems {
		fmt.Printf("  problem[%s/%s] span=%s: %s\n", problem.Code, problem.Severity, problem.SpanID, problem.Message)
	}
	fmt.Println()
}
