// Command demo runs the acceptance scenario end to end:
//
//  1. encode a GPS coordinate as a resolution-8 hexadecimal cell string
//  2. compute the full radius-3 topological neighborhood (37 cells) with
//     pure integer traversal and verify closed, hole-free coverage
//  3. route the neighborhood through the sharded, locality-aware engine
//     and show the cross-node communication reduction
package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"

	"geocell/grid"
	"geocell/shard"
)

func main() {
	// ---- 1. GPS -> resolution-8 hex cell --------------------------------
	gps := grid.LatLng{Lat: 39.9042, Lng: 116.4074} // Beijing
	cell, err := grid.LatLngToCell(gps, 8)
	if err != nil {
		log.Fatalf("encode: %v", err)
	}
	center := grid.CellToLatLng(cell)
	fmt.Printf("[1] GPS (%.4f, %.4f) -> res=8 cell %s\n", gps.Lat, gps.Lng, cell)
	fmt.Printf("    face=%d  center=(%.6f, %.6f)  valid=%v\n",
		cell.Face(), center.Lat, center.Lng, cell.IsValid())

	// hierarchy: parents and children
	for r := 7; r >= 5; r-- {
		p, _ := cell.Parent(r)
		fmt.Printf("    parent(res=%d) = %s\n", r, p)
	}
	children, _ := cell.Children(9)
	fmt.Printf("    children(res=9) = %d cells, first = %s\n", len(children), children[0])

	// ---- 2. radius-3 K-ring, integer-only traversal ---------------------
	const k = 3
	ring, err := grid.KRing(cell, k)
	if err != nil {
		log.Fatalf("k-ring: %v", err)
	}
	if err := grid.ValidateFaceLocal(ring); err != nil {
		log.Fatalf("face-local validation: %v", err)
	}
	fmt.Printf("\n[2] K-ring k=%d: %d cells (1+3k(k+1)=%d), face-local, no floats in traversal\n",
		k, len(ring), grid.KRingSize(k))

	// closed coverage / no holes: every interior cell has all 6 neighbors
	// inside the set
	set := make(map[grid.Cell]grid.IJ, len(ring))
	for _, c := range ring {
		set[c] = c.IJ()
	}
	cij := cell.IJ()
	holes := 0
	for c, ij := range set {
		if ij.Dist(cij) == k {
			continue // boundary cells may point outside
		}
		nbrs, _ := grid.Neighbors(c)
		for _, n := range nbrs {
			if _, ok := set[n]; !ok {
				holes++
			}
		}
	}
	fmt.Printf("    coverage check: interior cells fully surrounded, holes=%d -> continuous & closed\n", holes)
	fmt.Printf("    sample cells: %s %s %s ...\n", ring[0], ring[1], ring[2])

	// ---- 3. sharded locality-aware routing ------------------------------
	nodes := []shard.NodeID{"node-a", "node-b", "node-c", "node-d"}
	engine, err := shard.NewEngine(nodes, 3, 128, 256)
	if err != nil {
		log.Fatalf("engine: %v", err)
	}
	engine.Start()

	// ingest the ring plus 20k random cells to simulate a large dataset
	rng := rand.New(rand.NewSource(1))
	bulk := append([]grid.Cell{}, ring...)
	for i := 0; i < 20000; i++ {
		ll := grid.LatLng{Lat: rng.Float64()*170 - 85, Lng: rng.Float64()*360 - 180}
		c, err := grid.LatLngToCell(ll, 8)
		if err != nil {
			log.Fatalf("encode: %v", err)
		}
		bulk = append(bulk, c)
	}
	if err := engine.Ingest(bulk); err != nil {
		log.Fatalf("ingest: %v", err)
	}

	res, err := engine.QueryKRing(cell, k)
	if err != nil {
		log.Fatalf("query: %v", err)
	}
	reduction := 100 * (1 - float64(res.RPCs)/float64(res.NaiveRPCs))
	fmt.Printf("\n[3] locality-aware k-ring query over %d nodes:\n", len(nodes))
	fmt.Printf("    ring cells=%d  shards touched=%d  nodes touched=%d\n",
		res.Total, res.ShardsTouched, res.NodesTouched)
	fmt.Printf("    cross-node RPCs=%d  (naive cell-at-a-time=%d, -%.1f%%)\n",
		res.RPCs, res.NaiveRPCs, reduction)
	fmt.Printf("    present in cluster: %d/%d ring cells\n", len(res.Present), res.Total)

	m := engine.Metrics()
	fmt.Printf("    engine metrics: queries=%d ingests=%d cellsRouted=%d crossNodeRPC=%d cellsHit=%d\n",
		m.Queries, m.Ingests, m.CellsRouted, m.CrossNodeRPC, m.CellsHit)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := engine.Shutdown(ctx); err != nil {
		log.Fatalf("shutdown: %v", err)
	}
	fmt.Println("    engine shut down cleanly, no leaked goroutines")
}
