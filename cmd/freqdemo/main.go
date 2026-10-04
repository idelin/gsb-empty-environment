// Command freqdemo builds per-block summaries of a skewed stream and compares
// the error guarantees obtained from different merge orders, verifying every
// reported bound against an independent exact counter.
package main

import (
	"fmt"
	"math/rand"
	"sort"

	freq "freqsketch"
)

func main() {
	const (
		blocks   = 8
		capacity = 25
		n        = 100000
		universe = 2000
	)
	rng := rand.New(rand.NewSource(2024))
	parts := make([]*freq.Summary, blocks)
	for b := range parts {
		parts[b] = freq.New(capacity)
	}
	exact := freq.NewExact()
	for i := 0; i < n; i++ {
		// 60% of updates hit 5 hot keys; the rest form an exponential tail.
		var item string
		if rng.Float64() < 0.6 {
			item = fmt.Sprintf("hot-%d", rng.Intn(5))
		} else {
			item = fmt.Sprintf("key-%d", int(rng.ExpFloat64()*150)%universe)
		}
		w := uint64(1 + rng.Intn(3))
		parts[i%blocks].Update(item, w)
		exact.Update(item, w)
	}

	identity := make([]int, blocks)
	reversed := make([]int, blocks)
	for i := 0; i < blocks; i++ {
		identity[i] = i
		reversed[i] = blocks - 1 - i
	}
	shuffled := rand.New(rand.NewSource(11)).Perm(blocks)

	orders := []struct {
		name string
		perm []int
		tree bool
	}{
		{"sequential forward", identity, false},
		{"sequential reversed", reversed, false},
		{"sequential shuffled", shuffled, false},
		{"balanced   forward", identity, true},
		{"balanced   reversed", reversed, true},
		{"balanced   shuffled", shuffled, true},
	}

	fmt.Printf("stream: %d updates, %d distinct items, %d blocks x capacity %d\n\n",
		n, len(exact.Items()), blocks, capacity)
	fmt.Printf("%-22s %12s %8s %s\n", "merge order", "error bound", "tracked", "verified")
	for _, o := range orders {
		ordered := freq.Permuted(parts, o.perm)
		var m *freq.Summary
		if o.tree {
			m = freq.MergeBalanced(ordered)
		} else {
			m = freq.MergeSequential(ordered)
		}
		status := "OK"
		if err := freq.Verify(m, exact); err != nil {
			status = "FAIL: " + err.Error()
		}
		fmt.Printf("%-22s %12d %8d %s\n", o.name, m.Residual(), m.Size(), status)
	}

	// Show candidate estimates with bounds from the balanced-forward merge.
	m := freq.MergeBalanced(parts)
	type row struct {
		item   string
		lo, hi uint64
		exact  uint64
	}
	var rows []row
	for _, item := range m.Items() {
		lo, hi := m.Bounds(item)
		rows = append(rows, row{item, lo, hi, exact.Count(item)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].hi > rows[j].hi })
	fmt.Printf("\ntop candidates (balanced forward), f in [lo, hi]:\n")
	fmt.Printf("%-10s %10s %10s %10s\n", "item", "lo", "exact", "hi")
	for _, r := range rows {
		fmt.Printf("%-10s %10d %10d %10d\n", r.item, r.lo, r.exact, r.hi)
	}
}
