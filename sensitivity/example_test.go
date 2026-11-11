package sensitivity_test

import (
	"fmt"

	"example.org/netsec/q0435/sensitivity"
)

func Example() {
	events := &sensitivity.Source{ID: "events", Rel: "events",
		Cols: []string{"uid", "amount"}}
	users := &sensitivity.Source{ID: "users", Rel: "users",
		Cols: []string{"uid", "city"}}

	// 按 uid 做 1:1 唯一键连接，显式给出两侧重复上界。
	join := &sensitivity.Join{ID: "join", Left: events, Right: users,
		LeftKey: "uid", RightKey: "uid",
		LeftRepeat: sensitivity.NewRat(1), RightRepeat: sensitivity.NewRat(1)}

	// 对连接后的 amount 做截断求和。
	sum := &sensitivity.TruncatedSum{ID: "sum", In: join, Col: "amount",
		Lower: sensitivity.NewRat(0), Upper: sensitivity.NewRat(20)}

	trace, err := sensitivity.Derive(sum)
	if err != nil {
		panic(err)
	}
	fmt.Println(trace.Sens.String())

	tables := map[string]sensitivity.Table{
		"events": {Name: "events", Schema: []string{"uid", "amount"}, Rows: []sensitivity.Row{
			{Values: []sensitivity.Cell{sensitivity.NewRat(1), sensitivity.NewRat(50)}},
			{Values: []sensitivity.Cell{sensitivity.NewRat(2), sensitivity.NewRat(-5)}},
		}},
		"users": {Name: "users", Schema: []string{"uid", "city"}, Rows: []sensitivity.Row{
			{Values: []sensitivity.Cell{sensitivity.NewRat(1), sensitivity.NewRat(7)}},
			{Values: []sensitivity.Cell{sensitivity.NewRat(2), sensitivity.NewRat(7)}},
		}},
	}

	// 枚举删除任意一行、以及加入候选行的相邻输入，核验上界。
	report, err := sensitivity.Verify(sum, tables, map[string][]sensitivity.Row{
		"events": {{Values: []sensitivity.Cell{sensitivity.NewRat(3), sensitivity.NewRat(20)}}},
		"users":  {{Values: []sensitivity.Cell{sensitivity.NewRat(3), sensitivity.NewRat(9)}}},
	})
	if err != nil {
		panic(err)
	}
	fmt.Printf("邻居=%d 经验最大差值=%s 违规=%v\n",
		report.CheckedNeighbors, report.MaxScalarDelta.RatString(), report.Violation)

	// Output:
	// 标量≤20
	// 邻居=6 经验最大差值=20 违规=false
}
