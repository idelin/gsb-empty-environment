// demo 演示 RETE 网络的增量激活与负 Token 精确撤回。
package main

import (
	"fmt"

	"rete"
)

func printAgenda(step string, n *rete.Network) {
	agenda := n.Agenda()
	fmt.Printf("%s\n  冲突集(%d): ", step, len(agenda))
	if len(agenda) == 0 {
		fmt.Println("<空>")
	} else {
		fmt.Println()
		for _, a := range agenda {
			fmt.Printf("    %s\n", a)
		}
	}
	fmt.Printf("  Beta 网络驻留 Token 数: %d\n\n", n.TokenCount())
}

func main() {
	n := rete.NewNetwork()

	// 规则：25 岁用户的大额订单
	//   User(age=25, name=?user)  JOIN  Order(amount=100, user=?user)
	n.AddRule("YoungUserBigOrder",
		rete.Condition{
			Type:   "User",
			Consts: map[string]any{"age": 25},
			Binds:  map[string]string{"name": "?user"},
		},
		rete.Condition{
			Type:   "Order",
			Consts: map[string]any{"amount": 100},
			Binds:  map[string]string{"user": "?user"},
		},
	)
	fmt.Println("规则已加载: YoungUserBigOrder")
	fmt.Println("  User(age=25, name=?user) JOIN Order(amount=100, user=?user)")
	fmt.Println()

	user := n.AddFact("User", map[string]any{"age": 25, "name": "alice"})
	fmt.Printf(">>> 注入事实 %s\n", user)
	printAgenda("仅 User 到达，Beta Join 尚未满足:", n)

	order := n.AddFact("Order", map[string]any{"amount": 100, "user": "alice"})
	fmt.Printf(">>> 注入事实 %s\n", order)
	printAgenda("Order 到达，Beta 节点 Join 成功，规则被激活:", n)

	n.RetractFact(order.ID)
	fmt.Printf(">>> 撤销事实 %s\n", order)
	printAgenda("负 Token 沿网络级联，精确清除对应待激活条目:", n)

	n.RetractFact(user.ID)
	fmt.Printf(">>> 撤销事实 %s\n", user)
	printAgenda("User 亦被撤销，网络无残留 Token:", n)
}
