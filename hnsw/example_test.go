package hnsw_test

import (
	"fmt"
	"math"

	"prescreen.local/ps-eb477846fe/hnsw"
)

type point struct{ x, y float64 }

func Example() {
	distance := func(a, b point) float64 {
		return math.Hypot(a.x-b.x, a.y-b.y)
	}
	g, err := hnsw.NewGraph[point](hnsw.Config[point]{
		M:                     4, // M0 = 8 on layer 0
		Distance:              distance,
		KeepPrunedConnections: true,
	})
	if err != nil {
		panic(err)
	}

	// Import nodes: id, coordinates, maximum layer.
	must(g.AddNode(0, point{0, 0}, 1))
	must(g.AddNode(1, point{1, 0}, 1))
	must(g.AddNode(2, point{2, 0}, 0))
	must(g.AddNode(3, point{3, 0}, 0))

	// Import symmetric undirected edges per layer.
	must(g.AddEdge(1, 0, 1))
	for _, edge := range [][2]int{{0, 1}, {1, 2}, {2, 3}} {
		must(g.AddEdge(0, edge[0], edge[1]))
	}

	// Batch delete a high-connectivity hub; neighborhoods are reconstructed
	// with HNSW Algorithm 4, reverse edges re-shrunk, and the entry point
	// migrated when needed.
	report, err := g.Delete([]int{0})
	if err != nil {
		panic(err)
	}
	fmt.Println("deleted:", report.Deleted)

	if err := g.CheckInvariants(); err != nil {
		panic(err)
	}

	// Beam search skips tombstones and returns deterministic (distance, id)
	// ordering.
	hits, err := g.KNN(point{0.2, 0}, 2, 8)
	if err != nil {
		panic(err)
	}
	for _, hit := range hits {
		fmt.Println("hit:", hit.ID)
	}
	// Output:
	// deleted: [0]
	// hit: 1
	// hit: 2
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
