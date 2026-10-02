package main

import "fmt"

// 运行样例：对一张含数据倾斜的 users 表执行查询，
// 优化器使用失效统计信息（认为 users 仅 100 行），
// 闭环校验器逐算子比对估算与真实执行结果并输出告警。
func main() {
	users := BuildSkewedUsers()
	tables := map[string]*Table{"users": users}
	cat := StaleCatalog()

	// 查询: SELECT region, COUNT(*) FROM users WHERE age >= 30 GROUP BY region
	pred := func(r Row) bool { return r["age"].(int) >= 30 }
	root := PlanQuery(cat, pred, 0.5) // 直方图给出选择率 0.5

	est := EstPlan(root)
	act := Execute(root, tables)

	report, err := Compare(est, act)
	if err != nil {
		panic(err)
	}
	fmt.Println(report.String())
}
