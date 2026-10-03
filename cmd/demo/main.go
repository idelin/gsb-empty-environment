package main

import (
	"fmt"
	"sync"
	"sync/atomic"

	"hexgsb/arbiter"
	"hexgsb/hexgrid"
)

func main() {
	// --- Part 1: hierarchical hexagonal indexing ---
	ll := hexgrid.LatLng{Lat: 39.9042, Lng: 116.4074} // Beijing
	const res = 8
	cell, err := hexgrid.FromLatLng(ll, res)
	if err != nil {
		panic(err)
	}
	fmt.Printf("GPS (%.4f, %.4f) @ res=%d -> cell %s\n", ll.Lat, ll.Lng, res, cell)
	center, _ := cell.Center()
	fmt.Printf("cell center: (%.6f, %.6f)\n", center.Lat, center.Lng)

	disk, dists, err := cell.GridDiskDistances(3)
	if err != nil {
		panic(err)
	}
	hist := map[int]int{}
	for _, d := range dists {
		hist[d]++
	}
	ring, _ := cell.GridRing(3)
	fmt.Printf("k=3 disk: %d cells (per ring: %v), ring(3): %d cells\n", len(disk), hist, len(ring))

	parent, _ := cell.Parent(5)
	children, _ := parent.Children(8)
	fmt.Printf("parent@5: %s, children@8: %d, contains self: %v\n",
		parent, len(children), parent.IsAncestorOf(cell))

	// --- Part 2: multi-node contention & conflict arbitration ---
	arb := arbiter.New()
	const nodes = 8
	var wg sync.WaitGroup
	var wins int64
	for n := 0; n < nodes; n++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if _, err := arb.Acquire(fmt.Sprintf("node-%d", id), disk, 0); err == nil {
				atomic.AddInt64(&wins, 1)
			}
		}(n)
	}
	wg.Wait()
	snap, _ := arb.Snapshot(disk)
	owners := map[string]int{}
	for _, info := range snap {
		owners[info.Owner]++
	}
	fmt.Printf("concurrent claim of %d cells by %d nodes: winners=%d, ownership=%v\n",
		len(disk), nodes, wins, owners)
}
