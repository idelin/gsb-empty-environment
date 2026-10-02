// Command demo exercises the hexgrid library end to end: encoding a GPS
// coordinate, hierarchical parent/child scaling, trig-free K-ring
// traversal, and the zero-allocation cache + object pool hot paths.
package main

import (
	"fmt"
	"testing"
	"time"

	"hexgrid"
)

func main() {
	// --- Acceptance boundary: GPS -> hex string at resolution 8 ---------
	lat, lng := 39.9042, 116.4074 // Beijing
	const res = 8
	c := hexgrid.LatLngToCell(lat, lng, res)
	fmt.Printf("GPS (%.4f, %.4f) @ res %d  ->  %s\n", lat, lng, res, c)
	clat, clng := hexgrid.CellToLatLng(c)
	fmt.Printf("cell center                 ->  (%.6f, %.6f)\n", clat, clng)

	// --- Hierarchical parent/child scaling -------------------------------
	fmt.Println("parent chain (zoom out):")
	for r := res; r >= 0; r-- {
		fmt.Printf("  res %2d: %s\n", r, c.Parent(r))
	}
	children := c.Parent(4).Children()
	fmt.Printf("children of res-4 parent %s: %d cells, first = %s\n",
		c.Parent(4), len(children), children[0])

	// --- K-ring: all neighbours within radius 3 --------------------------
	pool := hexgrid.NewBufferPool()
	buf := pool.Get()
	disk, ok := hexgrid.GridDiskBuffered(c, 3, buf)
	if !ok {
		panic("disk overflow")
	}
	fmt.Printf("\nk=3 grid disk: %d cells (1+3k(k+1) = 37 expected)\n", len(disk))
	ring, _ := hexgrid.GridRingBuffered(c, 3, buf)
	fmt.Printf("k=3 hollow ring: %d cells (6k = 18 expected)\n", len(ring))
	fmt.Println("first 5 disk cells:")
	for i := 0; i < 5 && i < len(disk); i++ {
		d, _ := hexgrid.HexDistance(c, disk[i])
		fmt.Printf("  %s  (distance %d)\n", disk[i], d)
	}
	pool.Put(buf)

	// --- Zero-allocation cache -------------------------------------------
	var cache hexgrid.Cache
	key := hexgrid.CacheKey(lat, lng, res)
	cache.Put(key, uint64(c))
	if v, hit := cache.Get(key); hit {
		fmt.Printf("\ncache hit: %s (capacity %d, len %d)\n",
			hexgrid.Cell(v), cache.Capacity(), cache.Len())
	}

	// --- Allocation proof on hot paths -----------------------------------
	fmt.Println("\nzero-allocation proof (allocs/op):")
	fmt.Printf("  LatLngToCell:        %.0f\n", testing.AllocsPerRun(1000,
		func() { hexgrid.LatLngToCell(lat, lng, res) }))
	b2 := pool.Get()
	fmt.Printf("  GridDiskBuffered k=3: %.0f\n", testing.AllocsPerRun(1000,
		func() { hexgrid.GridDiskBuffered(c, 3, b2) }))
	pool.Put(b2)
	fmt.Printf("  Cache.Get (hit):      %.0f\n", testing.AllocsPerRun(1000,
		func() { cache.Get(key) }))

	// --- Throughput smoke test -------------------------------------------
	const n = 200000
	start := time.Now()
	var acc uint64
	for i := 0; i < n; i++ {
		acc += uint64(hexgrid.LatLngToCell(lat, lng, res))
	}
	el := time.Since(start)
	fmt.Printf("\nencode throughput: %d ops in %v (%.0f ns/op)\n",
		n, el, float64(el.Nanoseconds())/n)

	buf3 := pool.Get()
	start = time.Now()
	for i := 0; i < n/10; i++ {
		hexgrid.GridDiskBuffered(c, 3, buf3)
	}
	el = time.Since(start)
	pool.Put(buf3)
	fmt.Printf("k-ring throughput: %d ops in %v (%.0f ns/op)\n",
		n/10, el, float64(el.Nanoseconds())/(n/10))

	// --- Leak check -------------------------------------------------------
	gets, puts := pool.Stats()
	fmt.Printf("\npool stats: gets=%d puts=%d outstanding=%d (0 = no leak)\n",
		gets, puts, gets-puts)
	_ = acc
}
