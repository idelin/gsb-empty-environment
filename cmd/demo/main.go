// Command demo runs the adaptive batch-size regulator against a simulated
// cache hierarchy and prints how the batch size adapts to row width.
package main

import (
	"fmt"
	"time"

	"adaptive-batch/adaptivebatch"
)

const (
	l1Size     = 32 * 1024
	totalRows  = 1 << 20
	wideRow    = 128 // 16 x int64 columns: ultra-wide table
	narrowRow  = 8   // single int64 column
)

func main() {
	fmt.Println("=== Scenario 1: ultra-wide table scan (128 B/row, L1 = 32 KiB) ===")
	fmt.Println("acceptance: batch shrinks 1024 -> 256 so one batch fits exactly in L1")
	fmt.Println()
	runAdaptive(wideRow)

	fmt.Println()
	fmt.Println("--- baseline: fixed batch size 1024 (no regulator) ---")
	runFixed(wideRow, 1024)

	fmt.Println()
	fmt.Println("=== Scenario 2: narrow table scan (8 B/row) ===")
	fmt.Println("expectation: batch grows 1024 -> 4096 while the cache stays hot")
	fmt.Println()
	runAdaptive(narrowRow)
}

func runAdaptive(rowBytes int) {
	ctl := adaptivebatch.NewController()
	model := adaptivebatch.DefaultCacheModel()
	rep := adaptivebatch.RunScan(ctl, model, totalRows, rowBytes)

	fmt.Printf("%-6s %-8s %-12s %-10s %-12s %s\n",
		"batch#", "rows", "footprint", "L1-hit%", "batch-time", "fits-L1")
	for i, b := range rep.Batches {
		if i >= 8 && i < len(rep.Batches)-2 {
			if i == 8 {
				fmt.Println("  ... (steady state) ...")
			}
			continue
		}
		fmt.Printf("%-6d %-8d %-12s %-10.1f %-12s %v\n",
			i+1, b.BatchSize, humanBytes(b.Footprint), b.L1HitRatio*100,
			b.Duration, b.Footprint <= l1Size)
	}
	fmt.Printf("\ntotal: %d rows in %d batches, %s wall, %.1f M rows/s, final batch = %d\n",
		rep.TotalRows, len(rep.Batches), rep.TotalTime, rep.Throughput/1e6, rep.FinalBatch)
}

func runFixed(rowBytes, batchSize int) {
	model := adaptivebatch.DefaultCacheModel()
	var total time.Duration
	remaining := totalRows
	for remaining > 0 {
		size := batchSize
		if size > remaining {
			size = remaining
		}
		total += model.RunBatch(size, rowBytes).Duration
		remaining -= size
	}
	fmt.Printf("total: %d rows, %s wall, %.1f M rows/s (batch fixed at %d)\n",
		totalRows, total, float64(totalRows)/total.Seconds()/1e6, batchSize)
}

func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%d MiB", n/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KiB", n/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
