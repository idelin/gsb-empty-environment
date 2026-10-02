// Command demo exercises the hexgrid index and the arbiter end to end:
// encode a GPS coordinate at resolution 8, list its 3-ring neighborhood,
// then run a concurrent multi-node contention round on those cells.
package main

import (
	"fmt"
	"sync"
	"time"

	"hexgsb/arbiter"
	"hexgsb/hexgrid"
)

func main() {
	grid := hexgrid.NewGrid()

	// --- Part 1: hierarchical hexagon indexing ---
	lat, lon := 39.9042, 116.4074 // Beijing
	const res = 8
	cell := grid.FromLatLon(lat, lon, res)
	clat, clon := grid.Center(cell)
	fmt.Printf("GPS (%.4f, %.4f) @ res %d -> cell %s\n", lat, lon, res, cell)
	fmt.Printf("  rhombus=%d q=%d r=%d center=(%.4f, %.4f)\n",
		cell.Rhombus(), cell.Q(), cell.R(), clat, clon)

	parent, _ := cell.Parent()
	fmt.Printf("  parent(res %d) = %s\n", parent.Resolution(), parent)
	children, _ := parent.Children()
	fmt.Printf("  parent children:")
	for _, ch := range children {
		fmt.Printf(" %s", ch)
	}
	fmt.Println()

	// --- Part 2: pure-integer K-Ring traversal (k = 3) ---
	const k = 3
	disk := grid.KRing(cell, k)
	fmt.Printf("K-Ring k=%d: %d cells (expected %d), contiguous closed disk\n",
		k, len(disk), 1+3*k*(k+1))
	for i, c := range disk {
		fmt.Printf("  ring[%2d] %s\n", i, c)
		if i == 0 || i == 6 {
			fmt.Println("  ...")
		}
		if i > 6 {
			break
		}
	}

	// --- Part 3: multi-node contention & conflict arbitration ---
	arb := arbiter.New()
	cells := make([]arbiter.ID, len(disk))
	for i, c := range disk {
		cells[i] = arbiter.ID(c)
	}
	// 8 nodes compete for overlapping 7-cell windows of the same disk.
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := map[string]int{}
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			node := fmt.Sprintf("node-%d", n)
			for round := 0; round < 20; round++ {
				window := make([]arbiter.ID, 7)
				for j := range window {
					window[j] = cells[(n*7+j)%len(cells)]
				}
				g, err := arb.AcquireTimeout(node, window, 2*time.Second)
				if err != nil {
					continue
				}
				mu.Lock()
				wins[node]++
				mu.Unlock()
				time.Sleep(time.Millisecond) // hold the cells briefly
				g.Release()
			}
		}(n)
	}
	wg.Wait()
	fmt.Printf("arbiter: %d live grants after run (expect 0)\n", arb.Active())
	for n := 0; n < 8; n++ {
		node := fmt.Sprintf("node-%d", n)
		fmt.Printf("  %s won %d exclusive windows\n", node, wins[node])
	}
}
