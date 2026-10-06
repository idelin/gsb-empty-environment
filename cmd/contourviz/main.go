// Command contourviz extracts contours from a built-in sample image and
// prints regions, loops and the validation verdict.
package main

import (
	"fmt"

	"contour"
)

func main() {
	im := contour.ImageFromASCII(
		"##..###..#",
		"#.#..#..##",
		"##.####..#",
		"..#.#..##.",
		".#####.#..",
		"...#...#.#",
	)
	fmt.Println("image:")
	fmt.Print(im)

	res := contour.Extract(im)
	for _, r := range res.Regions {
		fmt.Printf("region %d: area=%d perimeter=%d holes=%d loops=%v\n",
			r.ID, r.Area, r.Perimeter, r.Holes, r.Loops)
	}
	for i, lp := range res.Loops {
		kind := "outer"
		if lp.Hole {
			kind = "hole"
		}
		fmt.Printf("loop %d: region=%d %s edges=%d self-touches=%v\n  points=%v\n",
			i, lp.Region, kind, lp.Len(), lp.SelfTouches(), lp.Points)
	}

	if err := contour.Validate(im, res); err != nil {
		fmt.Println("validation FAILED:")
		fmt.Println(err)
	} else {
		fmt.Println("validation OK: unit-edge counts, flood-fill background model and replay all agree")
	}
}
