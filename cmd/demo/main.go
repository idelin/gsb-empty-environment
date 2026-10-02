// demo 模拟一次 EXPLAIN ANALYZE 闭环校验：
// 含数据倾斜的查询，优化器因直方图失效严重低估 Scan(users) 行数。
package main

import (
	"fmt"
	"time"

	"reflector"
)

func main() {
	fmt.Println("查询: SELECT u.city, COUNT(*) FROM users u JOIN orders o ON o.user_id = u.id WHERE u.city = 'Beijing' GROUP BY u.city")
	fmt.Println()

	// 优化器事前估算（EXPLAIN）：users.city 直方图失效，按均匀分布估算。
	plan := &reflector.PlanNode{
		ID: "1", Operator: "Aggregate", EstRows: 1, EstCost: 220,
		Children: []*reflector.PlanNode{
			{
				ID: "2", Operator: "HashJoin", EstRows: 100, EstCost: 210,
				Children: []*reflector.PlanNode{
					{ID: "3", Operator: "Scan", Table: "users", EstRows: 100, EstCost: 10},
					{ID: "4", Operator: "Scan", Table: "orders", EstRows: 1000000, EstCost: 180},
				},
			},
		},
	}

	// 物理执行器真实统计（EXPLAIN ANALYZE）：'Beijing' 数据倾斜，实际 50000 行。
	actual := reflector.ActualPlan{
		"1": {ID: "1", ActRows: 1, ActTime: 900 * time.Millisecond, Loops: 1},
		"2": {ID: "2", ActRows: 50000, ActTime: 850 * time.Millisecond, Loops: 1},
		"3": {ID: "3", ActRows: 50000, ActTime: 300 * time.Millisecond, Loops: 1},
		"4": {ID: "4", ActRows: 1050000, ActTime: 400 * time.Millisecond, Loops: 1},
	}

	rep := reflector.NewComparator(reflector.DefaultOptions()).Compare(plan, actual)
	fmt.Print(rep.String())
}
