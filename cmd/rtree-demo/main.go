// Command rtree-demo builds an R-Tree over thousands of shop locations
// and runs a bounding-box (viewport) query, showing branch pruning.
package main

import (
	"fmt"
	"math/rand"
	"time"

	"rtree"
)

type shop struct {
	id   int
	name string
	lng  float64
	lat  float64
}

func main() {
	rng := rand.New(rand.NewSource(2026))

	// Simulate 20,000 shops clustered around four city centers.
	centers := [][2]float64{
		{121.47, 31.23}, // Shanghai
		{116.40, 39.90}, // Beijing
		{113.26, 23.13}, // Guangzhou
		{104.06, 30.57}, // Chengdu
	}
	const n = 20000
	shops := make([]shop, n)
	for i := range shops {
		c := centers[rng.Intn(len(centers))]
		shops[i] = shop{
			id:   i,
			name: fmt.Sprintf("shop-%05d", i),
			lng:  c[0] + rng.NormFloat64()*0.3,
			lat:  c[1] + rng.NormFloat64()*0.3,
		}
	}

	tree := rtree.New(16)
	start := time.Now()
	for _, s := range shops {
		tree.Insert(rtree.PointRect(s.lng, s.lat), s)
	}
	fmt.Printf("inserted %d shops in %v\n", n, time.Since(start))
	fmt.Printf("tree: height=%d, nodes=%d, size=%d\n",
		tree.Height(), tree.TotalNodes(), tree.Size())

	// Bounding-box query: a viewport over central Shanghai.
	window := rtree.Rect{MinX: 121.40, MinY: 31.18, MaxX: 121.55, MaxY: 31.28}
	start = time.Now()
	results, visited := tree.SearchWithStats(window)
	elapsed := time.Since(start)

	// Ground truth via brute-force scan.
	brute := 0
	for _, s := range shops {
		if s.lng >= window.MinX && s.lng <= window.MaxX &&
			s.lat >= window.MinY && s.lat <= window.MaxY {
			brute++
		}
	}

	fmt.Printf("\nwindow query %+v\n", window)
	fmt.Printf("found %d shops in %v (brute-force count: %d)\n",
		len(results), elapsed, brute)
	fmt.Printf("pruning: visited %d of %d nodes (%.1f%% of tree skipped)\n",
		visited, tree.TotalNodes(),
		100*float64(tree.TotalNodes()-visited)/float64(tree.TotalNodes()))

	fmt.Println("\nsample results:")
	for i := 0; i < 5 && i < len(results); i++ {
		s := results[i].(shop)
		fmt.Printf("  %s  lng=%.4f lat=%.4f\n", s.name, s.lng, s.lat)
	}
}
