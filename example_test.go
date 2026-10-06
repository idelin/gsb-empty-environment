package contour_test

import (
	"fmt"

	"contour"
)

func Example() {
	res := contour.Analyze(contour.FromRows(
		".##",
		"#.#",
		"###",
	))
	for _, reg := range res.Regions {
		fmt.Printf("region %d: area=%d perimeter=%d holes=%d\n",
			reg.ID, reg.Area, reg.Perimeter, reg.Holes)
	}
	for _, l := range res.Loops {
		fmt.Printf("  %s loop of region %d: start=%v moves=%s\n",
			l.Kind, l.Region, l.Points[0], l.Moves())
	}
	fmt.Println("discrepancies:", len(res.Validate()))
	// Output:
	// region 0: area=7 perimeter=16 holes=1
	//   outer loop of region 0: start={2 0} moves=WSWSSEEENNNW
	//   hole loop of region 0: start={1 1} moves=ESWN
	// discrepancies: 0
}
