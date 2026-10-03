// Command demo builds a small 3D scene, rasterizes it into a 64x64x64
// signed distance grid, and sphere-traces a few rays against both the
// analytic SDF and the sampled grid.
package main

import (
	"fmt"

	"github.com/example/sdf/sdf"
)

func main() {
	scene := sdf.Union(
		sdf.Sphere(sdf.Vec3{0, 0, 0}, 1),
		sdf.Box(sdf.Vec3{2.2, 0, 0}, sdf.Vec3{0.5, 0.5, 0.5}),
	)

	grid := sdf.Rasterize3(scene, 64, 64, 64,
		sdf.Vec3{-3, -3, -3}, sdf.Vec3{3, 3, 3})
	fmt.Printf("rasterized 64x64x64 grid: %d samples, sdf(origin)=%.3f sdf(0,0,2)=%.3f\n",
		len(grid.Data), grid.Sample(sdf.Vec3{0, 0, 0}), grid.Sample(sdf.Vec3{0, 0, 2}))

	rays := []struct {
		origin, dir sdf.Vec3
	}{
		{sdf.Vec3{0, 0, 5}, sdf.Vec3{0, 0, -1}},
		{sdf.Vec3{5, 3, 2}, sdf.Vec3{-5, -3, -2}},
		{sdf.Vec3{2.2, 5, 0}, sdf.Vec3{0, -1, 0}},
		{sdf.Vec3{0, 0, 5}, sdf.Vec3{0, 1, 0}}, // miss
	}
	for i, r := range rays {
		h := sdf.RayMarch3(scene, r.origin, r.dir, sdf.MarchOptions{})
		hg := sdf.RayMarchGrid3(grid, r.origin, r.dir, sdf.MarchOptions{Epsilon: 1e-3})
		fmt.Printf("ray %d: analytic hit=%v t=%.3f steps=%d | grid hit=%v t=%.3f steps=%d\n",
			i, h.Hit, h.Distance, h.Steps, hg.Hit, hg.Distance, hg.Steps)
		if h.Hit {
			n := grid.Gradient(h.Point).Norm()
			fmt.Printf("        hit point %v, grid normal (%.2f, %.2f, %.2f)\n",
				h.Point, n.X, n.Y, n.Z)
		}
	}
}
