// Command demo encodes a GPS coordinate at resolution 8, expands its
// k=3 neighborhood disk, and routes the cells through the sharding engine.
package main

import (
	"fmt"

	"hexgsb/hexgrid"
	"hexgsb/shard"
)

func main() {
	lat, lng := 39.9042, 116.4074 // Beijing
	const res = 8
	origin := hexgrid.Encode(lat, lng, res)
	fmt.Printf("GPS (%.4f, %.4f) @ res=%d -> cell %s\n", lat, lng, res, origin)

	clat, clng := origin.Center()
	fmt.Printf("cell center: (%.6f, %.6f), approx area: %.3f km^2\n",
		clat, clng, origin.ApproxAreaKm2())

	disk := hexgrid.KRingDisk(origin, 3)
	ring := hexgrid.KRing(origin, 3)
	fmt.Printf("k=3 disk: %d cells (closed, hole-free), outer ring: %d cells\n",
		len(disk), len(ring))

	var area float64
	for _, c := range disk {
		area += c.ApproxAreaKm2()
	}
	fmt.Printf("disk coverage area: %.2f km^2\n", area)

	nodes := []string{"node-a", "node-b", "node-c", "node-d",
		"node-e", "node-f", "node-g", "node-h"}
	router := shard.NewRouter(4096, 5, nodes)
	plan := router.Route(disk)
	fmt.Printf("routed %d cells via %d virtual shard(s) to %d node(s):\n",
		plan.Cells, plan.Shards, len(plan.Assignments))
	for _, n := range router.Nodes() {
		fmt.Printf("  %s: %d cells\n", n, len(plan.Assignments[n]))
	}
	total, cross := router.LocalityScore(disk)
	fmt.Printf("adjacent pairs: %d, cross-node pairs: %d (%.1f%%)\n",
		total, cross, 100*float64(cross)/float64(total))
}
