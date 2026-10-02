package rete

import "testing"

// 验收边界：注入 User(age=25) 与 Order(amount=100)，Beta 节点匹配成功激活规则；
// 撤销 Order 后，负 Token 精准清除冲突集中的对应待激活条目。
func TestAcceptanceAssertThenRetract(t *testing.T) {
	net := NewNetwork()
	net.AddRule(Rule{
		Name: "young-user-big-order",
		Patterns: []Pattern{
			{Type: "User", Conds: []Condition{{Field: "age", Op: "==", Value: 25}}},
			{Type: "Order", Conds: []Condition{{Field: "amount", Op: ">=", Value: 100}}},
		},
	})

	user := net.Assert("User", map[string]interface{}{"age": 25})
	if got := len(net.ConflictSet()); got != 0 {
		t.Fatalf("只注入 User 时冲突集应为空, 实际 %d 条", got)
	}

	order := net.Assert("Order", map[string]interface{}{"amount": 100})
	cs := net.ConflictSet()
	if len(cs) != 1 {
		t.Fatalf("注入 Order 后冲突集应有 1 条激活, 实际 %d 条", len(cs))
	}
	act := cs[0]
	if act.Rule != "young-user-big-order" {
		t.Fatalf("激活的规则名错误: %s", act.Rule)
	}
	facts := act.Token.Facts()
	if len(facts) != 2 || facts[0] != user || facts[1] != order {
		t.Fatalf("激活 Token 应精确绑定 User 与 Order, 实际 %v", facts)
	}

	if !net.Retract(order.ID) {
		t.Fatal("撤销 Order 应返回 true")
	}
	if got := len(net.ConflictSet()); got != 0 {
		t.Fatalf("撤销 Order 后冲突集应为空, 实际 %d 条", got)
	}

	// 精确级联：中间 Beta 记忆里的 User Token 必须保留，末端记忆必须清空。
	join1 := net.root.joins[0]
	join2 := join1.out.joins[0]
	if got := len(join1.out.tokens); got != 1 {
		t.Fatalf("撤销 Order 不应波及 User Token, 中间记忆应有 1 个 Token, 实际 %d", got)
	}
	if got := len(join2.out.tokens); got != 0 {
		t.Fatalf("末端 Beta 记忆应被清空, 实际 %d 个 Token", got)
	}
	if got := len(net.factTokens[order.ID]); got != 0 {
		t.Fatalf("Order 的 Token 索引应被清除, 实际 %d", got)
	}
	if len(net.factTokens[user.ID]) != 1 {
		t.Fatalf("User 的 Token 索引应保留 1 条, 实际 %d", len(net.factTokens[user.ID]))
	}
}

// Alpha 网络：常量条件不满足的事实不得进入 Alpha 记忆，也不得产生 Token。
func TestAlphaConstantFiltering(t *testing.T) {
	net := NewNetwork()
	net.AddRule(Rule{
		Name: "adult",
		Patterns: []Pattern{
			{Type: "User", Conds: []Condition{
				{Field: "age", Op: ">=", Value: 18},
				{Field: "country", Op: "==", Value: "CN"},
			}},
		},
	})

	net.Assert("User", map[string]interface{}{"age": 10, "country": "CN"})
	net.Assert("User", map[string]interface{}{"age": 30, "country": "US"})
	net.Assert("Order", map[string]interface{}{"amount": 999}) // 无关类型
	if got := len(net.ConflictSet()); got != 0 {
		t.Fatalf("未命中常量条件不应激活, 实际 %d 条", got)
	}
	if got := len(net.alphas[0].memory); got != 0 {
		t.Fatalf("Alpha 记忆应为空, 实际 %d 条", got)
	}

	ok := net.Assert("User", map[string]interface{}{"age": 20, "country": "CN"})
	if got := len(net.ConflictSet()); got != 1 {
		t.Fatalf("命中常量条件应激活 1 条, 实际 %d", got)
	}
	net.Retract(ok.ID)
	if got := len(net.ConflictSet()); got != 0 {
		t.Fatalf("撤销后冲突集应为空, 实际 %d", got)
	}
}

// Beta 网络：跨事实 Join 测试（Order.userID == User.id），
// 且撤销中间事实需级联清除下游 Token 与激活。
func TestBetaJoinAndCascadeRetract(t *testing.T) {
	net := NewNetwork()
	net.AddRule(Rule{
		Name: "user-order-item",
		Patterns: []Pattern{
			{Type: "User", Conds: []Condition{{Field: "age", Op: ">=", Value: 18}}},
			{Type: "Order", JoinTests: []JoinTest{{LeftIndex: 0, LeftField: "id", RightField: "userID"}}},
			{Type: "Item", JoinTests: []JoinTest{{LeftIndex: 1, LeftField: "id", RightField: "orderID"}}},
		},
	})

	u1 := net.Assert("User", map[string]interface{}{"id": 1, "age": 25})
	u2 := net.Assert("User", map[string]interface{}{"id": 2, "age": 40})
	o1 := net.Assert("Order", map[string]interface{}{"id": 10, "userID": 1})
	net.Assert("Order", map[string]interface{}{"id": 11, "userID": 999}) // 无匹配用户
	i1 := net.Assert("Item", map[string]interface{}{"id": 100, "orderID": 10})

	cs := net.ConflictSet()
	if len(cs) != 1 {
		t.Fatalf("Join 后应只有 1 条激活, 实际 %d", len(cs))
	}
	facts := cs[0].Token.Facts()
	if len(facts) != 3 || facts[0] != u1 || facts[1] != o1 || facts[2] != i1 {
		t.Fatalf("Token 绑定错误: %v", facts)
	}

	// 撤销中间事实 Order：Item Token 与激活被级联清除，User Token 不受影响。
	net.Retract(o1.ID)
	if got := len(net.ConflictSet()); got != 0 {
		t.Fatalf("撤销 Order 后冲突集应为空, 实际 %d", got)
	}
	join1 := net.root.joins[0]
	join2 := join1.out.joins[0]
	join3 := join2.out.joins[0]
	if got := len(join1.out.tokens); got != 2 {
		t.Fatalf("两个 User Token 都应保留, 实际 %d", got)
	}
	if got := len(join2.out.tokens); got != 0 {
		t.Fatalf("Order 层 Token 应清空, 实际 %d", got)
	}
	if got := len(join3.out.tokens); got != 0 {
		t.Fatalf("Item 层 Token 应被级联清空, 实际 %d", got)
	}
	_ = u2
}

// 撤销不存在的事实应安全返回 false，网络状态不变。
func TestRetractUnknownFact(t *testing.T) {
	net := NewNetwork()
	net.AddRule(Rule{
		Name:     "r",
		Patterns: []Pattern{{Type: "User"}},
	})
	net.Assert("User", map[string]interface{}{"age": 1})
	if net.Retract(999) {
		t.Fatal("撤销不存在的事实应返回 false")
	}
	if got := len(net.ConflictSet()); got != 1 {
		t.Fatalf("网络状态不应改变, 冲突集应有 1 条, 实际 %d", got)
	}
}

// 多条规则共享 Alpha 节点：相同常量条件只建一个 Alpha 节点。
func TestAlphaNodeSharing(t *testing.T) {
	net := NewNetwork()
	cond := []Condition{{Field: "age", Op: "==", Value: 25}}
	net.AddRule(Rule{Name: "r1", Patterns: []Pattern{{Type: "User", Conds: cond}}})
	net.AddRule(Rule{Name: "r2", Patterns: []Pattern{{Type: "User", Conds: cond}}})
	if got := len(net.alphas); got != 1 {
		t.Fatalf("相同条件应共享 Alpha 节点, 实际 %d 个", got)
	}
	u := net.Assert("User", map[string]interface{}{"age": 25})
	if got := len(net.ConflictSet()); got != 2 {
		t.Fatalf("两条规则都应激活, 实际 %d", got)
	}
	net.Retract(u.ID)
	if got := len(net.ConflictSet()); got != 0 {
		t.Fatalf("撤销后两条激活都应清除, 实际 %d", got)
	}
}
