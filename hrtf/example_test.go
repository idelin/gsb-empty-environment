package hrtf_test

import (
	"fmt"

	"example.org/domainreissue/q055/hrtf"
)

// tetraMesh builds a simple tetrahedral spherical mesh. Every vertex carries
// the same delay-removed FIR but an ear-specific integer delay.
func tetraMesh() (*hrtf.Mesh, error) {
	dirs := []hrtf.Vec3{
		{1, 1, 1}, {-1, -1, 1}, {-1, 1, -1}, {1, -1, -1},
	}
	verts := make([]hrtf.Vertex, 4)
	for i := range verts {
		verts[i] = hrtf.Vertex{
			Dir:   dirs[i],
			Left:  hrtf.Filter{FIR: []float64{1, 0.5}, Delay: 4 + i},
			Right: hrtf.Filter{FIR: []float64{1, 0.5}, Delay: 8 - i},
		}
	}
	faces := []hrtf.Face{
		{ID: 0, A: 0, B: 1, C: 2},
		{ID: 1, A: 0, B: 3, C: 1},
		{ID: 2, A: 0, B: 2, C: 3},
		{ID: 3, A: 1, B: 3, C: 2},
	}
	return hrtf.NewMesh(verts, faces)
}

func ExampleEngine() {
	mesh, err := tetraMesh()
	if err != nil {
		panic(err)
	}
	eng := hrtf.NewEngine(mesh)
	left := eng.NewConvolver(hrtf.Left)
	right := eng.NewConvolver(hrtf.Right)

	// One direction update drives both ears at the same sampling instant.
	dir := hrtf.Vec3{0, 0, 1}
	if err := eng.Update(dir, left, right); err != nil {
		panic(err)
	}

	block := make([]float64, 16)
	block[0] = 1 // unit impulse
	outL := left.ProcessBlock(block)
	outR := right.ProcessBlock(block)

	firstPeak := func(x []float64) int {
		for i, v := range x {
			if v != 0 {
				return i
			}
		}
		return -1
	}

	l, r, err := eng.Interpolate(dir)
	if err != nil {
		panic(err)
	}
	fmt.Printf("left delay=%.1f right delay=%.1f\n", l.Delay, r.Delay)
	fmt.Printf("left first tap at %d, right first tap at %d\n",
		firstPeak(outL), firstPeak(outR))
	// Output:
	// left delay=4.5 right delay=7.5
	// left first tap at 4, right first tap at 7
}
