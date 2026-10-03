// Command demo triangulates 50 random survey points and prints the compact
// topology: triangle adjacency, convex hull, and a verification of the empty
// circumcircle property.
package main

import (
	"fmt"
	"math"
	"math/rand"

	"delaunay"
)

func main() {
	r := rand.New(rand.NewSource(2026))
	pts := make([]delaunay.Point, 50)
	for i := range pts {
		pts[i] = delaunay.Point{X: r.Float64() * 100, Y: r.Float64() * 100}
	}

	mesh := delaunay.Triangulate(pts)

	fmt.Printf("input points        : %d\n", len(mesh.Points))
	fmt.Printf("triangles           : %d\n", len(mesh.Triangles))
	fmt.Printf("convex hull (%2d pts) : %v\n", len(mesh.Hull), mesh.Hull)

	// Verify the Empty Circumcircle Property.
	violations := 0
	for _, tr := range mesh.Triangles {
		a, b, c := mesh.Points[tr.V[0]], mesh.Points[tr.V[1]], mesh.Points[tr.V[2]]
		center, r2 := delaunay.Circumcircle(a, b, c)
		for j, p := range mesh.Points {
			if j == tr.V[0] || j == tr.V[1] || j == tr.V[2] {
				continue
			}
			dx, dy := p.X-center.X, p.Y-center.Y
			if dx*dx+dy*dy < r2-1e-9*(r2+1) {
				violations++
			}
		}
	}
	fmt.Printf("circumcircle violations: %d\n", violations)

	// Mesh quality: min/max triangle angles (uniformity indicator).
	minA, maxA := 180.0, 0.0
	for _, tr := range mesh.Triangles {
		p := [3]delaunay.Point{mesh.Points[tr.V[0]], mesh.Points[tr.V[1]], mesh.Points[tr.V[2]]}
		d := func(i, j int) float64 {
			dx, dy := p[i].X-p[j].X, p[i].Y-p[j].Y
			return dx*dx + dy*dy
		}
		for i := 0; i < 3; i++ {
			a, b, c := d((i+1)%3, (i+2)%3), d(i, (i+1)%3), d(i, (i+2)%3)
			cos := (b + c - a) / (2 * math.Sqrt(b*c))
			ang := math.Acos(math.Min(1, math.Max(-1, cos))) * 180 / math.Pi
			minA = math.Min(minA, ang)
			maxA = math.Max(maxA, ang)
		}
	}
	fmt.Printf("angle range         : %.2f deg .. %.2f deg\n", minA, maxA)
	fmt.Printf("Euler check         : T=%d, 2N-2-H=%d\n", len(mesh.Triangles), 2*len(mesh.Points)-2-len(mesh.Hull))

	fmt.Println("\nsample of compact topology (first 5 triangles):")
	for i := 0; i < 5 && i < len(mesh.Triangles); i++ {
		tr := mesh.Triangles[i]
		fmt.Printf("  T%-2d vertices=%v neighbours=%v\n", i, tr.V, tr.N)
	}
}
