package delaunay

import (
	"math/rand"
	"testing"
)

func TestStress(t *testing.T) {
	for seed := int64(100); seed < 160; seed++ {
		pts := randomPoints(300, seed)
		tri := NewBuilder(pts...).Triangulation()
		if !tri.IsDelaunay() {
			t.Fatalf("seed %d: not Delaunay", seed)
		}
		checkTopology(t, tri)
		checkHull(t, tri)
	}
	// clustered points + far outliers
	for seed := int64(0); seed < 20; seed++ {
		r := rand.New(rand.NewSource(seed))
		var pts []Point
		for i := 0; i < 200; i++ {
			pts = append(pts, Point{50 + r.Float64(), 50 + r.Float64()})
		}
		pts = append(pts, Point{0, 0}, Point{1000, 500}, Point{0, 1000})
		tri := NewBuilder(pts...).Triangulation()
		if !tri.IsDelaunay() {
			t.Fatalf("cluster seed %d: not Delaunay", seed)
		}
		checkTopology(t, tri)
		checkHull(t, tri)
	}
}
