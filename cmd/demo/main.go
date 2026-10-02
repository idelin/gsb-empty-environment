// Command demo exercises the acceptance flow: encode a GPS coordinate at
// resolution 8, compute its 3-ring neighborhood, verify continuous closed
// coverage, and compare cached vs. direct lookup latency.
package main

import (
	"fmt"
	"time"

	"hexgrid"
)

func main() {
	lat, lng := 39.9042, 116.4074 // Beijing
	const res = 8
	const k = 3

	cell := hexgrid.CellFromLatLng(lat, lng, res)
	fmt.Printf("GPS (%.4f, %.4f) @ res %d\n", lat, lng, res)
	fmt.Printf("  cell:   %s  (face %d)\n", cell, cell.Face())
	clat, clng := cell.LatLng()
	fmt.Printf("  center: %.6f, %.6f\n", clat, clng)
	fmt.Printf("  parent@5: %s  children@9: %d\n",
		cell.Parent(5), len(cell.Children(9)))

	disk := hexgrid.GridDisk(cell, k)
	fmt.Printf("\nk=%d disk: %d cells (1+3k(k+1)=%d)\n", k, len(disk), 1+3*k*(k+1))
	for i := 0; i < 6 && i+1 < len(disk); i++ {
		fmt.Printf("  ring1[%d] = %s\n", i, disk[1+i])
	}

	// Coverage check: every cell within distance k-1 of the center must
	// have all six neighbors inside the disk (closed, no holes).
	inDisk := make(map[hexgrid.Cell]bool, len(disk))
	for _, c := range disk {
		inDisk[c] = true
	}
	var buf [8]hexgrid.Cell
	holes := 0
	for _, c := range hexgrid.GridDisk(cell, k-1) {
		n := hexgrid.GridDiskInto(c, 1, buf[:])
		for i := 0; i < n; i++ {
			if !inDisk[buf[i]] {
				holes++
			}
		}
	}
	fmt.Printf("coverage: closed=%v holes=%d\n", holes == 0, holes)

	// Zero-allocation cache latency comparison.
	dc := hexgrid.NewDiskCache(8, 3)
	defer dc.Close()
	const rounds = 200000
	diskBuf := make([]hexgrid.Cell, 64)

	start := time.Now()
	for i := 0; i < rounds; i++ {
		hexgrid.GridDiskInto(cell, k, diskBuf)
	}
	direct := time.Since(start)

	dc.GridDiskInto(cell, k, diskBuf) // warm the cache
	start = time.Now()
	for i := 0; i < rounds; i++ {
		dc.GridDiskInto(cell, k, diskBuf)
	}
	cached := time.Since(start)

	fmt.Printf("\nlatency (%d rounds, k=%d):\n", rounds, k)
	fmt.Printf("  direct GridDiskInto: %.0f ns/op\n", float64(direct.Nanoseconds())/rounds)
	fmt.Printf("  cached hit path:     %.0f ns/op\n", float64(cached.Nanoseconds())/rounds)
}
