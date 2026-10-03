// Command sdfdemo demonstrates the full pipeline: build an analytic
// scene, rasterize it into a 64x64 (2D) and 64x64x64 (3D) signed
// distance field, sphere-trace rays through both the analytic field
// and the rasterized grids, and extract iso-contours / iso-surfaces.
package main

import (
	"fmt"
	"time"

	"sdf"
)

func main() {
	demo2D()
	demo3D()
}

func demo2D() {
	fmt.Println("=== 2D: 64x64 signed distance field ===")
	scene := sdf.Union2(
		sdf.Circle{Center: sdf.V2(-0.35, 0), Radius: 0.55},
		sdf.Union2(
			sdf.Box2{Center: sdf.V2(0.55, 0.25), Half: sdf.V2(0.35, 0.25)},
			sdf.Segment2D{A: sdf.V2(-0.6, -0.75), B: sdf.V2(0.8, -0.55), Radius: 0.12},
		),
	)
	lo, hi := sdf.V2(-1.6, -1.6), sdf.V2(1.6, 1.6)

	start := time.Now()
	grid := sdf.Sample2(scene, 64, 64, lo, hi)
	fmt.Printf("rasterized 64x64 grid in %v (%d samples)\n\n",
		time.Since(start).Round(time.Microsecond), len(grid.Values))

	printGridASCII(grid)

	segs := grid.Contour(0)
	fmt.Printf("\nmarching squares: extracted %d contour segments (iso=0)\n", len(segs))

	// Ray march the rasterized grid from an arbitrary origin.
	ro := sdf.V2(1.45, 1.35)
	rd := sdf.V2(-1, -0.9).Normalize()
	res := sdf.RayMarch2(grid, ro, rd, sdf.MarchOptions{StepScale: 0.9})
	fmt.Printf("\nray march on grid: origin=%v dir=%v\n", ro, rd)
	fmt.Printf("  hit=%v steps=%d t=%.4f point=%v\n",
		res.Hit, res.Steps, res.T, res.Position)
	fmt.Printf("  sdf at hit point = %.5f (surface)\n", scene.Distance(res.Position))
	fmt.Println()
}

func demo3D() {
	fmt.Println("=== 3D: 64x64x64 signed distance field ===")
	scene := sdf.Subtract3(
		sdf.Union3(
			sdf.Sphere{Center: sdf.V3(0, 0, 0), Radius: 1},
			sdf.Box3{Center: sdf.V3(1.3, 0, 0), Half: sdf.V3(0.5, 0.5, 0.5)},
		),
		sdf.Sphere{Center: sdf.V3(1.3, 0, 0), Radius: 0.3},
	)
	lo, hi := sdf.V3(-2, -2, -2), sdf.V3(2.4, 2, 2)

	start := time.Now()
	grid := sdf.Sample3(scene, 64, 64, 64, lo, hi)
	fmt.Printf("rasterized 64x64x64 grid in %v (%d samples)\n",
		time.Since(start).Round(time.Microsecond), len(grid.Values))

	// Sphere-trace the analytic field.
	ro := sdf.V3(4, 1.2, 0.8)
	rd := sdf.V3(-1, -0.3, -0.2).Normalize()
	resA := sdf.RayMarch3(scene, ro, rd, sdf.MarchOptions{})
	fmt.Printf("\nray march (analytic field): origin=%v\n", ro)
	fmt.Printf("  hit=%v steps=%d t=%.4f point=%v\n",
		resA.Hit, resA.Steps, resA.T, resA.Position)
	fmt.Printf("  surface normal = %v\n", sdf.Normal3(scene, resA.Position))

	// Sphere-trace the rasterized grid itself.
	resG := sdf.RayMarch3(grid, ro, rd, sdf.MarchOptions{StepScale: 0.9})
	fmt.Printf("ray march (rasterized 64^3 grid):\n")
	fmt.Printf("  hit=%v steps=%d t=%.4f point=%v\n",
		resG.Hit, resG.Steps, resG.T, resG.Position)

	start = time.Now()
	tris := grid.Surface(0)
	fmt.Printf("\nmarching tetrahedra: extracted %d triangles (iso=0) in %v\n",
		len(tris), time.Since(start).Round(time.Microsecond))

	// Mesh SDF: a closed triangle mesh queried as a signed distance field.
	mesh := sdf.CubeMesh(sdf.V3(0, 0, 0), 1)
	fmt.Printf("\nmesh SDF (cube, 12 triangles):\n")
	fmt.Printf("  distance at (3,0,0)   = %+.3f (outside)\n", mesh.Distance(sdf.V3(3, 0, 0)))
	fmt.Printf("  distance at (0,0,0)   = %+.3f (inside)\n", mesh.Distance(sdf.V3(0, 0, 0)))
	resM := sdf.RayMarch3(mesh, sdf.V3(5, 0.3, -0.2), sdf.V3(-1, 0, 0), sdf.MarchOptions{})
	fmt.Printf("  ray march on mesh: hit=%v steps=%d point=%v\n",
		resM.Hit, resM.Steps, resM.Position)
}

// printGridASCII renders the 64x64 field as shaded ASCII art.
func printGridASCII(g *sdf.Grid2) {
	const shades = " .:-=+*#%@"
	for j := g.Ny - 1; j >= 0; j-- {
		row := make([]byte, g.Nx)
		for i := 0; i < g.Nx; i++ {
			v := g.At(i, j)
			switch {
			case v < 0:
				row[i] = '@' // inside
			case v < 0.06:
				row[i] = '*' // surface band (zero level set)
			default:
				// exterior fades from dense (near surface) to empty
				t := v / 0.6
				if t > 1 {
					t = 1
				}
				row[i] = shades[int((1-t)*float64(len(shades)-2))]
			}
		}
		fmt.Println(string(row))
	}
}
