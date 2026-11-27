package windowframe_test

import (
	"fmt"

	"prescreen.local/ps-f6cb3f0fdc/windowframe"
)

func ExampleEvaluate() {
	rows := [][]windowframe.Value{
		{windowframe.Int(1), windowframe.Int(10)},
		{windowframe.Int(1), windowframe.Int(20)},
		{windowframe.Int(2), windowframe.Int(30)},
	}
	spec := windowframe.WindowSpec{
		PartitionColumns: []int{0},
		Order:            []windowframe.OrderSpec{{Column: 1}},
	}
	out, err := windowframe.Evaluate(rows, spec, windowframe.FuncExpr{
		Func:   windowframe.FuncSum,
		Column: 1,
	})
	if err != nil {
		panic(err)
	}
	for _, v := range out {
		fmt.Println(v.AsInt())
	}
	// Output:
	// 10
	// 30
	// 30
}

func ExampleEvaluate_rangeWithExclude() {
	rows := [][]windowframe.Value{
		{windowframe.Int(5)},
		{windowframe.Int(5)},
		{windowframe.Int(9)},
	}
	end := windowframe.UnboundedFollowing()
	spec := windowframe.WindowSpec{
		Order: []windowframe.OrderSpec{{Column: 0}},
		Frame: &windowframe.FrameSpec{
			Mode:      windowframe.RowsFrame,
			Start:     windowframe.UnboundedPreceding(),
			End:       &end,
			Exclusion: windowframe.ExcludeTies,
		},
	}
	out, err := windowframe.Evaluate(rows, spec, windowframe.FuncExpr{
		Func:     windowframe.FuncCount,
		CountAll: true,
	})
	if err != nil {
		panic(err)
	}
	for _, v := range out {
		fmt.Println(v.AsInt())
	}
	// Output:
	// 2
	// 2
	// 3
}
