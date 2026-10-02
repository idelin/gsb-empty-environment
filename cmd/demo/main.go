// Command demo simulates a vectorized scan over an ultra-wide table and a
// narrow table, printing how the adaptive batch-size regulator reacts.
package main

import (
	"fmt"

	"adaptivebatch"
)

func report(title string, t adaptivebatch.Table) {
	cfg := adaptivebatch.DefaultConfig()
	ex := adaptivebatch.NewExecutor(cfg)
	stats := ex.Run(t)

	fmt.Printf("=== %s ===\n", title)
	fmt.Printf("table=%s rows=%d rowWidth=%dB L1=%dKiB\n\n",
		t.Name, t.NumRows, t.RowWidthBytes, cfg.L1SizeBytes/1024)
	fmt.Printf("%-6s %-10s %-12s %-10s\n", "batch#", "rows", "footprint", "L1 hit")
	shown := 0
	for _, tr := range stats.Traces {
		// Print the transition region only: first batches until the size
		// has been stable for a while.
		if shown < 6 {
			fmt.Printf("%-6d %-10d %-12s %-10.2f\n",
				tr.Index, tr.BatchSize, humanBytes(tr.Footprint), tr.L1HitRate)
			shown++
		}
	}
	fmt.Printf("...\nconverged: batch=%d rows (footprint=%s, L1=%s), batches=%d, avg L1 hit=%.3f\n\n",
		stats.FinalBatch,
		humanBytes(stats.FinalBatch*t.RowWidthBytes),
		humanBytes(cfg.L1SizeBytes),
		stats.Batches, stats.AvgL1HitRate)
}

func humanBytes(n int) string {
	if n >= 1024 {
		return fmt.Sprintf("%dKiB", n/1024)
	}
	return fmt.Sprintf("%dB", n)
}

func main() {
	// Acceptance scenario: ultra-wide table, 128-byte rows. A 1024-row
	// batch needs 128 KiB and thrashes the 32 KiB L1; the regulator
	// steps down 1024 -> 512 -> 256 so one batch fits exactly in L1.
	report("ultra-wide table scan (memory-bandwidth bound)",
		adaptivebatch.Table{Name: "ultra_wide", NumRows: 1 << 20, RowWidthBytes: 128})

	// Extremely narrow rows: the regulator grows the batch to 4096.
	report("narrow table scan",
		adaptivebatch.Table{Name: "narrow", NumRows: 1 << 20, RowWidthBytes: 4})
}
