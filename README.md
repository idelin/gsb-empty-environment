# 隔离域设备重置依赖

设备交给另一个租户前，需要按依赖顺序复位共享功能块。写供Go宿主调用的重置规划包，功能块有共享资源边及复位前置条件，最少复位集合必须切断旧租户全部可观察状态路径。最多二十块，精确求最低成本集合并生成顺序；驱动任一步失败则保持隔离。用穷举集合与模拟设备验证。

## 用法

```go
spec := netsec.Spec{
    Blocks: []netsec.Block{
        {Name: "dma", Cost: 10, Tainted: true},      // 持有旧租户状态
        {Name: "buf", Cost: 1},                       // 共享资源中间块
        {Name: "nic", Cost: 10, Observable: true},    // 新租户可观察
    },
    Edges: []netsec.Edge{{From: 0, To: 1}, {From: 1, To: 2}},
    Pre:   [][]int{nil, {0}, nil}, // 复位 buf 前必须先复位 dma
}
plan, err := netsec.PlanReset(spec) // 精确求最低成本复位集合与顺序
if err != nil { /* 无可行计划 */ }
err = netsec.Execute(plan, driver)  // 任一步失败即 Abort 保持隔离
```

- `PlanReset`：位掩码子集枚举（≤20 块），对前置条件取传递闭包，
  最小化闭包总成本；输出 `Cut`（核心割集）、`Set`（含闭包）、
  `Order`（拓扑序）与 `Cost`。
- `Execute`：按 `Order` 调用 `Driver.Reset`，全部成功才 `Commit`
  解除隔离；任一步或 Commit 失败即 `Abort`，设备保持隔离。
- 测试：随机规格与独立暴力枚举交叉验证最优性（`plan_test.go`），
  模拟设备验证复位后无残留泄漏及失败隔离语义（`execute_test.go`）。
