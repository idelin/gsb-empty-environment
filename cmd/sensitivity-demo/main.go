// sensitivity-demo 用一个小数据集演示敏感度推导与相邻输入核验，
// 输出逐节点推导树和核验报告，供安全统计编译器宿主复核。
package main

import (
	"fmt"

	"example.org/netsec/q0435/sensitivity"
)

func main() {
	events := &sensitivity.Source{ID: "events", Rel: "events",
		Cols: []string{"uid", "amount"}}
	users := &sensitivity.Source{ID: "users", Rel: "users",
		Cols: []string{"uid", "city"}}

	join := &sensitivity.Join{ID: "join", Left: events, Right: users,
		LeftKey: "uid", RightKey: "uid",
		LeftRepeat: sensitivity.NewRat(1), RightRepeat: sensitivity.NewRat(1)}
	projected := &sensitivity.Project{ID: "project", In: join, Drop: []string{"city"}}
	sum := &sensitivity.TruncatedSum{ID: "sum", In: projected, Col: "amount",
		Lower: sensitivity.NewRat(0), Upper: sensitivity.NewRat(20)}

	trace, err := sensitivity.Derive(sum)
	if err != nil {
		panic(err)
	}
	fmt.Println("==== 逐节点敏感度推导 ====")
	fmt.Print(trace.Render())

	tables := map[string]sensitivity.Table{
		"events": {Name: "events", Schema: []string{"uid", "amount"}, Rows: []sensitivity.Row{
			{Values: []sensitivity.Cell{sensitivity.NewRat(1), sensitivity.NewRat(50)}},
			{Values: []sensitivity.Cell{sensitivity.NewRat(2), sensitivity.NewRat(-5)}},
			{Values: []sensitivity.Cell{sensitivity.NewRat(3), sensitivity.NewRat(7)}},
		}},
		"users": {Name: "users", Schema: []string{"uid", "city"}, Rows: []sensitivity.Row{
			{Values: []sensitivity.Cell{sensitivity.NewRat(1), sensitivity.NewRat(7)}},
			{Values: []sensitivity.Cell{sensitivity.NewRat(2), sensitivity.NewRat(7)}},
			{Values: []sensitivity.Cell{sensitivity.NewRat(3), sensitivity.NewRat(9)}},
		}},
	}
	extra := map[string][]sensitivity.Row{
		"events": {{Values: []sensitivity.Cell{sensitivity.NewRat(4), sensitivity.NewRat(20)}}},
		"users":  {{Values: []sensitivity.Cell{sensitivity.NewRat(4), sensitivity.NewRat(9)}}},
	}

	report, err := sensitivity.Verify(sum, tables, extra)
	if err != nil {
		panic(err)
	}
	fmt.Println("\n==== 相邻输入核验 ====")
	fmt.Print(report.Render())

	// 对照：去掉右重复上界，连接敏感度应为未知并向下游传播。
	join.RightRepeat = nil
	unknown, err := sensitivity.Derive(sum)
	if err != nil {
		panic(err)
	}
	fmt.Println("\n==== 缺少重复上界 ====")
	fmt.Println(unknown.Sens.String())
}
