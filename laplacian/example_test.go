package laplacian_test

import (
	"fmt"

	"example.org/domainreissue/q028/laplacian"
)

func ExampleDeform() {
	// A single triangle; every component needs at least one anchor.
	mesh := &laplacian.Mesh{
		Vertices: []laplacian.Vec3{
			{0, 0, 0},
			{1, 0, 0},
			{0, 1, 0},
		},
		Faces: [][3]int{{0, 1, 2}},
	}

	result, err := laplacian.Deform(mesh,
		[]int{0, 1},
		[]laplacian.Vec3{{0, 0, 1}, {1, 0, 1}},
	)
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Printf("anchors satisfied: %.2e\n", result.AnchorMax)
	fmt.Printf("vertex 2 z = %.3f\n", result.Positions[2].Z)
	// Output:
	// anchors satisfied: 0.00e+00
	// vertex 2 z = 1.000
}
