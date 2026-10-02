// Command demo builds a Maglev lookup table for 5 backends, shows the slot
// distribution, kills the 2nd node, rebuilds, and reports key drift and
// lookup latency.
package main

import (
	"fmt"
	"time"

	"maglev-demo/maglev"
)

func main() {
	backends := []string{"node-0", "node-1", "node-2", "node-3", "node-4"}

	start := time.Now()
	before, err := maglev.Build(backends, maglev.DefaultSize)
	if err != nil {
		panic(err)
	}
	fmt.Printf("built table: size=%d (prime), backends=%d, build time=%v\n",
		before.Size(), len(backends), time.Since(start))

	fmt.Println("\nslot distribution (ideal = 65537/5 = 13107.4):")
	for i, c := range before.Distribution() {
		fmt.Printf("  %-8s %6d slots  (%.4f%%)\n", backends[i], c,
			float64(c)/float64(before.Size())*100)
	}

	// Kill the 2nd node and rebuild.
	killed := backends[1]
	survivors := append([]string{}, backends[:1]...)
	survivors = append(survivors, backends[2:]...)
	start = time.Now()
	after, err := maglev.Build(survivors, maglev.DefaultSize)
	if err != nil {
		panic(err)
	}
	fmt.Printf("\nkilled %s, rebuilt table in %v\n", killed, time.Since(start))
	fmt.Printf("slots that changed owner: %.4f%% (theoretical minimum 20.0000%%)\n",
		maglev.ChangedSlots(before, after)*100)

	// Simulate a key space and measure drift of surviving keys.
	const numKeys = 200000
	onKilled, drifted, onSurvivors := 0, 0, 0
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("user-session-%d", i)
		oldOwner := before.Lookup(key)
		if oldOwner == killed {
			onKilled++
			continue
		}
		onSurvivors++
		if after.Lookup(key) != oldOwner {
			drifted++
		}
	}
	fmt.Printf("\nkey simulation over %d keys:\n", numKeys)
	fmt.Printf("  keys on killed node : %d (%.2f%%) — must be remapped\n",
		onKilled, float64(onKilled)/numKeys*100)
	fmt.Printf("  keys on survivors   : %d, drifted: %d (drift rate %.4f%%)\n",
		onSurvivors, drifted, float64(drifted)/float64(onSurvivors)*100)

	// Measure O(1) lookup latency (keys pre-generated so we time Lookup only).
	const probes = 5000000
	keys := make([]string, 1024)
	for i := range keys {
		keys[i] = fmt.Sprintf("probe-%d", i)
	}
	start = time.Now()
	var sink string
	for i := 0; i < probes; i++ {
		sink = after.Lookup(keys[i%len(keys)])
	}
	_ = sink
	elapsed := time.Since(start)
	fmt.Printf("\nlookup latency: %d probes in %v -> %.1f ns/op (constant time)\n",
		probes, elapsed, float64(elapsed.Nanoseconds())/probes)
}
