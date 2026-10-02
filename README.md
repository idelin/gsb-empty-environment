# rete — 增量规则 RETE 网络模式匹配器

纯 Go 标准库实现的 RETE 模式匹配算法核心，无任何第三方依赖。

## 结构

- **Alpha 网络**（`AlphaNode`）：对单条事实做常量条件过滤（`==` `!=` `<` `<=` `>` `>=`），
  命中的事实缓存于 Alpha 记忆；相同条件的规则共享同一 Alpha 节点。
- **Beta 网络**（`JoinNode` + `BetaMemory`）：Join 节点左接 Beta 记忆、右接 Alpha 记忆，
  支持跨事实 Join 测试（`JoinTest` 比较 Token 中历史事实与新事实的字段）。
- **增量激活**：`Assert` 时新事实沿网络传播（`rightActivate`/`leftActivate`），
  末端 Token 激活规则进入冲突集（`ConflictSet`）。
- **负 Token 级联撤回**：`Retract` 通过 `factTokens` 索引定位直接依赖该事实的 Token，
  沿 `children` 链精确级联删除整棵 Token 子树，并同步移除冲突集中的待激活条目；
  不相关的 Token 原地保留，**全程不重新扫描事实库**。

## 运行样例

```sh
go run ./cmd/demo
```

输出：

```
断言 User#1map[age:25]
[只注入 User] 冲突集大小 = 0
断言 Order#2map[amount:100]
[注入 Order 后] 冲突集大小 = 1
  -> Activation(young-user-big-order, facts=[User#1map[age:25] Order#2map[amount:100]])
撤销 Order#2map[amount:100]
[撤销 Order 后] 冲突集大小 = 0
```

## 测试

```sh
go test ./... -v
```

结果（全部通过）：

```
=== RUN   TestAcceptanceAssertThenRetract   # 验收边界：User(age=25)+Order(amount=100) 激活，撤销 Order 精准清除
--- PASS: TestAcceptanceAssertThenRetract (0.00s)
=== RUN   TestAlphaConstantFiltering        # Alpha 常量过滤
--- PASS: TestAlphaConstantFiltering (0.00s)
=== RUN   TestBetaJoinAndCascadeRetract     # Beta 跨事实 Join + 三级级联撤回
--- PASS: TestBetaJoinAndCascadeRetract (0.00s)
=== RUN   TestRetractUnknownFact            # 撤销不存在事实的安全性
--- PASS: TestRetractUnknownFact (0.00s)
=== RUN   TestAlphaNodeSharing              # 多规则共享 Alpha 节点
--- PASS: TestAlphaNodeSharing (0.00s)
PASS
ok  	rete
```

## API 速览

```go
net := rete.NewNetwork()
net.AddRule(rete.Rule{
    Name: "young-user-big-order",
    Patterns: []rete.Pattern{
        {Type: "User",  Conds: []rete.Condition{{Field: "age", Op: "==", Value: 25}}},
        {Type: "Order", Conds: []rete.Condition{{Field: "amount", Op: ">=", Value: 100}}},
    },
})
user  := net.Assert("User",  map[string]interface{}{"age": 25})
order := net.Assert("Order", map[string]interface{}{"amount": 100})
net.ConflictSet()   // -> 1 条 Activation
net.Retract(order.ID)
net.ConflictSet()   // -> 空
```
