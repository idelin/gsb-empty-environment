// 运行样例：演示验收场景中的补录、分裂与历史回溯查询。
package main

import (
	"fmt"

	"orgtemporal"
)

func main() {
	s := orgtemporal.NewStore()

	fmt.Println("== 录入 ==")
	must(s.Record("E", "M1", "D1", 1, 10, 1))
	fmt.Println("知悉1: E 业务[1,10) 汇报 M1, 部门 D1")
	must(s.Record("E", "M2", "D2", 3, 7, 5))
	fmt.Println("知悉5: 补录 E 业务[3,7) 汇报 M2, 部门 D2")

	fmt.Println("\n== 知悉5 时 E 的最新认知时间线（自动分裂后）==")
	for _, a := range s.Segments("E", 5) {
		fmt.Println("  ", a)
	}

	fmt.Println("\n== 历史回溯查询 ==")
	show := func(knowledge, business int) {
		mgr, _ := s.ManagerAt("E", knowledge, business)
		dept, _ := s.DepartmentAt("E", knowledge, business)
		fmt.Printf("  知悉%d 查询业务%d: 汇报 %s, 部门 %s\n", knowledge, business, mgr, dept)
	}
	show(6, 4) // M2
	show(6, 8) // M1
	show(4, 4) // M1
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
