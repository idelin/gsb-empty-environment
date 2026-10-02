# rete — 增量 RETE 模式匹配器（Go 标准库）

## 结构

- `rete.go` — RETE 核心：
  - **Alpha 网络**：`AlphaNode` 按「事实类型 + 属性常量测试」过滤，命中事实驻留 `Memory`；相同常量测试的条件共享节点。
  - **Beta 网络**：`BetaNode` 将父级 BetaMemory 的 `Token` 与 AlphaMemory 的事实按 `JoinTest`（变量绑定相等性）做跨事实 Join，末端 `Production` 把匹配物化为冲突集（Agenda）中的 `Activation`。
  - **增量激活**：`AddFact` 只沿 Alpha → Beta 网络传播，O(网络路径) 而非全库匹配。
  - **负 Token 撤回**：`RetractFact` 从 AlphaMemory 摘除事实后注入 `negToken`，沿 Beta 网络按「引用该事实 / 引用被撤父 Token」精确级联删除下游 Token 与冲突集条目，不做全量重扫。
- `rete_test.go` — `go test` 测试（含验收边界场景）。
- `cmd/demo/main.go` — 运行样例。

## 运行

```sh
go test ./... -v   # 测试
go run ./cmd/demo  # 演示：注入 User(age=25)/Order(amount=100) → 激活 → 撤销 Order → 负 Token 清除
```

## 验收场景

规则 `YoungUserBigOrder`：`User(age=25, name=?user) JOIN Order(amount=100, user=?user)`

1. 注入 `User(age=25, name=alice)` —— 冲突集为空（Join 未满足）。
2. 注入 `Order(amount=100, user=alice)` —— Beta Join 成功，冲突集出现 `[YoungUserBigOrder]?user=alice`。
3. 撤销 Order —— 负 Token 级联，冲突集精确清空，仅 User 侧 Token 保留。
