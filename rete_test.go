package rete

import "testing"

// youngUserBigOrder 是验收场景使用的规则：
// User(age=25, name=?user) JOIN Order(amount=100, user=?user)
func youngUserBigOrder(n *Network) *Production {
	return n.AddRule("YoungUserBigOrder",
		Condition{
			Type:   "User",
			Consts: map[string]any{"age": 25},
			Binds:  map[string]string{"name": "?user"},
		},
		Condition{
			Type:   "Order",
			Consts: map[string]any{"amount": 100},
			Binds:  map[string]string{"user": "?user"},
		},
	)
}

// TestAcceptanceScenario 验收边界：
// 注入 User(age=25) 与 Order(amount=100) -> Beta 节点 Join 成功激活规则；
// 撤销 Order -> 负 Token 精确清除冲突集中对应的待激活条目。
func TestAcceptanceScenario(t *testing.T) {
	n := NewNetwork()
	youngUserBigOrder(n)

	user := n.AddFact("User", map[string]any{"age": 25, "name": "alice"})
	if got := len(n.Agenda()); got != 0 {
		t.Fatalf("仅注入 User 后冲突集应为空, 实际 %d 条", got)
	}

	order := n.AddFact("Order", map[string]any{"amount": 100, "user": "alice"})
	agenda := n.Agenda()
	if len(agenda) != 1 {
		t.Fatalf("注入 Order 后冲突集应有 1 条待激活, 实际 %d 条", len(agenda))
	}
	act := agenda[0]
	if act.Rule != "YoungUserBigOrder" {
		t.Fatalf("激活的规则名错误: %s", act.Rule)
	}
	if act.Token.bindings["?user"] != "alice" {
		t.Fatalf("绑定变量 ?user 应为 alice, 实际 %v", act.Token.bindings["?user"])
	}

	if !n.RetractFact(order.ID) {
		t.Fatal("撤销 Order 应返回 true")
	}
	if got := len(n.Agenda()); got != 0 {
		t.Fatalf("撤销 Order 后冲突集应为空, 实际 %d 条", got)
	}
	if got := n.TokenCount(); got != 1 {
		// 只剩 User 条件对应 Beta 节点中的 1 个 Token
		t.Fatalf("撤销后 Beta 网络应只剩 User 的 1 个 Token, 实际 %d", got)
	}

	// 再撤销 User，网络应完全干净。
	if !n.RetractFact(user.ID) {
		t.Fatal("撤销 User 应返回 true")
	}
	if got := n.TokenCount(); got != 0 {
		t.Fatalf("全部撤销后 Beta 网络应无残留 Token, 实际 %d", got)
	}
}

// TestAlphaConstantFilter Alpha 网络常量过滤：只有 age=25 的 User 进入匹配。
func TestAlphaConstantFilter(t *testing.T) {
	n := NewNetwork()
	n.AddRule("Only25",
		Condition{Type: "User", Consts: map[string]any{"age": 25}, Binds: map[string]string{"name": "?u"}},
	)

	n.AddFact("User", map[string]any{"age": 30, "name": "bob"})
	n.AddFact("User", map[string]any{"age": 25, "name": "alice"})
	n.AddFact("Product", map[string]any{"age": 25, "name": "widget"}) // 类型不匹配

	agenda := n.Agenda()
	if len(agenda) != 1 {
		t.Fatalf("常量过滤后应只有 1 条激活, 实际 %d", len(agenda))
	}
	if agenda[0].Token.bindings["?u"] != "alice" {
		t.Fatalf("命中的应是 alice, 实际 %v", agenda[0].Token.bindings["?u"])
	}
}

// TestBetaJoinMismatch Join 键不匹配时不产生激活。
func TestBetaJoinMismatch(t *testing.T) {
	n := NewNetwork()
	youngUserBigOrder(n)

	n.AddFact("User", map[string]any{"age": 25, "name": "alice"})
	n.AddFact("Order", map[string]any{"amount": 100, "user": "bob"})  // user 不匹配
	n.AddFact("Order", map[string]any{"amount": 50, "user": "alice"}) // amount 被 Alpha 过滤

	if got := len(n.Agenda()); got != 0 {
		t.Fatalf("Join 键不匹配/常量过滤后冲突集应为空, 实际 %d 条", got)
	}
}

// TestRetractCascadePrecise 负 Token 精确级联：
// 多条激活共存时，撤销一条 Order 只移除对应条目，其余不受影响。
func TestRetractCascadePrecise(t *testing.T) {
	n := NewNetwork()
	youngUserBigOrder(n)

	n.AddFact("User", map[string]any{"age": 25, "name": "alice"})
	o1 := n.AddFact("Order", map[string]any{"amount": 100, "user": "alice"})
	n.AddFact("Order", map[string]any{"amount": 100, "user": "alice"})

	if got := len(n.Agenda()); got != 2 {
		t.Fatalf("应产生 2 条激活, 实际 %d", got)
	}

	n.RetractFact(o1.ID)
	agenda := n.Agenda()
	if len(agenda) != 1 {
		t.Fatalf("撤销一条 Order 后应剩 1 条激活, 实际 %d", len(agenda))
	}
	if agenda[0].Token.fact.ID == o1.ID {
		t.Fatal("残留的激活不应引用已撤销的 Order")
	}
}

// TestRetractLeftCascade 撤销上游事实（User）时，负 Token 沿左侧级联，
// 清除所有依赖它的下游 Token 与激活。
func TestRetractLeftCascade(t *testing.T) {
	n := NewNetwork()
	youngUserBigOrder(n)

	user := n.AddFact("User", map[string]any{"age": 25, "name": "alice"})
	n.AddFact("Order", map[string]any{"amount": 100, "user": "alice"})
	n.AddFact("Order", map[string]any{"amount": 100, "user": "alice"})

	if got := len(n.Agenda()); got != 2 {
		t.Fatalf("应产生 2 条激活, 实际 %d", got)
	}

	n.RetractFact(user.ID)
	if got := len(n.Agenda()); got != 0 {
		t.Fatalf("撤销 User 后冲突集应为空, 实际 %d", got)
	}
	if got := n.TokenCount(); got != 0 {
		t.Fatalf("撤销 User 后 Beta 网络应无残留 Token, 实际 %d", got)
	}
}

// TestRetractUnknownFact 撤销不存在的事实是安全的空操作。
func TestRetractUnknownFact(t *testing.T) {
	n := NewNetwork()
	youngUserBigOrder(n)
	n.AddFact("User", map[string]any{"age": 25, "name": "alice"})
	n.AddFact("Order", map[string]any{"amount": 100, "user": "alice"})

	if n.RetractFact(9999) {
		t.Fatal("撤销不存在的事实应返回 false")
	}
	if got := len(n.Agenda()); got != 1 {
		t.Fatalf("空操作不应影响冲突集, 实际 %d 条", got)
	}
}

// TestIncrementalRuleAddition 规则后于事实加入时，
// 已有事实沿新建子网增量 seed，而非重新扫描。
func TestIncrementalRuleAddition(t *testing.T) {
	n := NewNetwork()
	n.AddFact("User", map[string]any{"age": 25, "name": "alice"})
	n.AddFact("Order", map[string]any{"amount": 100, "user": "alice"})

	youngUserBigOrder(n)
	if got := len(n.Agenda()); got != 1 {
		t.Fatalf("规则后加入时应由存量事实增量激活 1 条, 实际 %d", got)
	}
}
