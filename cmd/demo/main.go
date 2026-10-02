// 演示：增量断言 User/Order 激活规则，再撤销 Order 观察负 Token 级联撤回。
package main

import (
	"fmt"

	"rete"
)

func printAgenda(tag string, net *rete.Network) {
	cs := net.ConflictSet()
	fmt.Printf("[%s] 冲突集大小 = %d\n", tag, len(cs))
	for _, a := range cs {
		fmt.Printf("  -> %s\n", a)
	}
}

func main() {
	net := rete.NewNetwork()
	net.AddRule(rete.Rule{
		Name: "young-user-big-order",
		Patterns: []rete.Pattern{
			{Type: "User", Conds: []rete.Condition{{Field: "age", Op: "==", Value: 25}}},
			{Type: "Order", Conds: []rete.Condition{{Field: "amount", Op: ">=", Value: 100}}},
		},
	})

	user := net.Assert("User", map[string]interface{}{"age": 25})
	fmt.Printf("断言 %v\n", user)
	printAgenda("只注入 User", net)

	order := net.Assert("Order", map[string]interface{}{"amount": 100})
	fmt.Printf("断言 %v\n", order)
	printAgenda("注入 Order 后", net)

	net.Retract(order.ID)
	fmt.Printf("撤销 %v\n", order)
	printAgenda("撤销 Order 后", net)
}
