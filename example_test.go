package frsimp_test

import (
	"fmt"

	"frsimp"
)

func ExampleSimplify() {
	points := []frsimp.Point{{0, 0}, {5, 0}, {5, 0.5}, {0, 0.5}}
	res, err := frsimp.Simplify(points, frsimp.Options{Epsilon: 1})
	if err != nil {
		panic(err)
	}
	fmt.Println("kept indices:", res.Indices)
	fmt.Println("optimal:", res.Optimal)
	fmt.Printf("witnessed distance: %.1f\n", res.Distance)
	// Output:
	// kept indices: [0 1 3]
	// optimal: true
	// witnessed distance: 0.5
}
