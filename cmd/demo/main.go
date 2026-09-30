package main

import (
	"fmt"

	"telemetry"
)

func main() {
	store := telemetry.NewStore(60) // 60-unit fixed buckets
	telemetry.Replay(store, telemetry.SampleScript())

	fmt.Println("== query [1000, 1400) at current version ==")
	printResult(store.Query(1_000, 1_400))

	fmt.Println("\n== same range as of version 2 (before correction & delete) ==")
	printResult(store.QueryAsOf(1_000, 1_400, 2))

	fmt.Println("\n== audit log ==")
	for _, a := range store.Audit() {
		fmt.Printf("v%d %-7s range [%d,%d) buckets=%v details=%d events in [%d,%d]\n",
			a.Version, a.Op, a.Start, a.End, a.Buckets, a.DetailCount, a.DetailLo, a.DetailHi)
	}
}

func printResult(r telemetry.Result) {
	mean, ok := r.Mean()
	fmt.Printf("count=%d sum=%.2f mean_ok=%v", r.Count, r.Sum, ok)
	if ok {
		fmt.Printf(" mean=%.4f", mean)
	}
	fmt.Println()
	if r.Count > 0 {
		fmt.Printf("max evidence: id=%s ts=%d value=%.2f\n", r.Max.ID, r.Max.TS, r.Max.Value)
		fmt.Printf("min evidence: id=%s ts=%d value=%.2f\n", r.Min.ID, r.Min.TS, r.Min.Value)
		fmt.Printf("first: id=%s ts=%d  last: id=%s ts=%d\n", r.First.ID, r.First.TS, r.Last.ID, r.Last.TS)
	}
	for _, b := range r.Buckets {
		state := "gap"
		if b.Present {
			state = fmt.Sprintf("count=%d sum=%.2f max=%.2f", b.Summary.Count, b.Summary.Sum, b.Summary.Max.Value)
		}
		partial := ""
		if b.Partial {
			partial = " (edge/detail)"
		}
		fmt.Printf("  bucket %d @%d: %s%s\n", b.Bucket, b.Start, state, partial)
	}
}
