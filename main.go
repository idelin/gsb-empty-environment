// Command maglev-demo builds a Maglev lookup table for 5 nodes, shows the
// slot distribution, then kills node-1, rebuilds, and reports the no-drift
// ratio and lookup timing.
package main

import (
	"fmt"
	"time"

	"maglev/maglev"
)

func main() {
	nodes := []string{"node-0", "node-1", "node-2", "node-3", "node-4"}

	tbl, err := maglev.Build(nodes, maglev.DefaultTableSize)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Built Maglev table: %d slots, %d nodes\n", tbl.Size(), len(nodes))
	for _, n := range tbl.Nodes() {
		c := tbl.Distribution()[n]
		fmt.Printf("  %-8s %6d slots (%.4f%%)\n", n, c, 100*float64(c)/float64(tbl.Size()))
	}

	// Kill the 2nd node and rebuild.
	killed := "node-1"
	survivors := make([]string, 0, len(nodes)-1)
	for _, n := range nodes {
		if n != killed {
			survivors = append(survivors, n)
		}
	}
	rebuilt, err := maglev.Build(survivors, maglev.DefaultTableSize)
	if err != nil {
		panic(err)
	}
	fmt.Printf("\nKilled %s, rebuilt table with %d nodes\n", killed, len(survivors))
	for _, n := range rebuilt.Nodes() {
		c := rebuilt.Distribution()[n]
		fmt.Printf("  %-8s %6d slots (%.4f%%)\n", n, c, 100*float64(c)/float64(rebuilt.Size()))
	}

	unchanged := 0
	for slot := 0; slot < tbl.Size(); slot++ {
		if tbl.Nodes()[tbl.Entry(slot)] == rebuilt.Nodes()[rebuilt.Entry(slot)] {
			unchanged++
		}
	}
	fmt.Printf("\nNo-drift slots: %d/%d = %.4f%% (theory: %.4f%%)\n",
		unchanged, tbl.Size(),
		100*float64(unchanged)/float64(tbl.Size()),
		100*float64(len(survivors))/float64(len(nodes)))

	// Lookup timing.
	const lookups = 5000000
	keys := make([]string, 1024)
	for i := range keys {
		keys[i] = fmt.Sprintf("session-%d", i)
	}
	start := time.Now()
	for i := 0; i < lookups; i++ {
		_ = rebuilt.Lookup(keys[i%len(keys)])
	}
	elapsed := time.Since(start)
	fmt.Printf("Lookups: %d in %v, avg %v per lookup (O(1))\n", lookups, elapsed, elapsed/lookups)

	fmt.Printf("\nSample: %q -> %s\n", "session-42", rebuilt.Lookup("session-42"))
}
