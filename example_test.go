package taskpkg

import "fmt"

func ExamplePlan() {
	result, err := Plan(Request{
		Blocks: []Block{
			{ID: "heading", Heading: true, Rect: RectFromLTRB(20, 10, 580, 50)},
			{ID: "left", Rect: RectFromLTRB(20, 80, 260, 160)},
			{ID: "right", Rect: RectFromLTRB(340, 80, 580, 160)},
		},
		CandidateLines: []CandidateLine{{ID: "gutter", X: 300}},
		Direction:      DirectionLTR,
	})
	if err != nil {
		panic(err)
	}

	fmt.Println(result.Order)
	fmt.Println(result.SelectedLineIDs)

	// Output:
	// [heading left right]
	// [gutter]
}
