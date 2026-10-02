// demo 演示验收边界场景：补录历史调岗后的双时态查询。
package main

import (
	"fmt"
	"strings"

	"orgtree"
)

func main() {
	tree := orgtree.New()

	fmt.Println("== 录入事件 ==")
	fmt.Println("知悉 1: E 在业务 [1,10) 汇报给 M1")
	must(tree.Assign("E", "M1", 1, 10, 1))
	fmt.Println("知悉 5: 补录 E 在业务 [3,7) 汇报给 M2")
	must(tree.Assign("E", "M2", 3, 7, 5))

	fmt.Println("\n== 双时态查询（员工 E 的直接汇报对象）==")
	for _, q := range []struct{ valid, sys int }{
		{4, 6}, {8, 6}, {4, 4},
	} {
		manager, ok := tree.ManagerAt("E", q.valid, q.sys)
		if !ok {
			fmt.Printf("知悉 %d 查询业务 %d -> 无汇报关系\n", q.sys, q.valid)
			continue
		}
		fmt.Printf("知悉 %d 查询业务 %d -> %s\n", q.sys, q.valid, manager)
	}

	fmt.Println("\n== 汇报链查询 ==")
	for _, q := range []struct{ valid, sys int }{
		{4, 6}, {8, 6}, {4, 4},
	} {
		chain, err := tree.ChainAt("E", q.valid, q.sys)
		if err != nil {
			fmt.Printf("知悉 %d 查询业务 %d -> 错误: %v\n", q.sys, q.valid, err)
			continue
		}
		fmt.Printf("知悉 %d 查询业务 %d -> E -> %s\n", q.sys, q.valid, strings.Join(chain, " -> "))
	}

	fmt.Println("\n== 审计视图（全部记录版本）==")
	for _, r := range tree.Records() {
		fmt.Printf("%s 业务[%d,%d) 汇报给 %-2s | 知悉[%d,%s)\n",
			r.Employee, r.ValidFrom, r.ValidTo, r.Manager, r.SysFrom, sysTo(r.SysTo))
	}
}

func sysTo(v int) string {
	if v == orgtree.Infinity {
		return "∞"
	}
	return fmt.Sprint(v)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
