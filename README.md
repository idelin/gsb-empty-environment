# compensation

Go 实现的依赖图补偿执行器（saga 模式）：正向按拓扑序执行外部步骤，任一
步骤失败后，对所有已完成步骤（失败步骤的祖先以及已完成的并行分支）生成
**可恢复的补偿计划**，按依赖逆序执行补偿。

## 特性

- **依赖图执行**：`Graph` 校验重复 ID、缺失依赖与环，按确定性拓扑序执行。
- **全量补偿**：失败时补偿所有已完成步骤，不限于失败步骤的祖先链。
- **逆序补偿**：执行顺序是拓扑序，反转后保证先补偿依赖者、再补偿被依赖者。
- **失败可重试**：`RetryPolicy{MaxAttempts, Backoff}` 控制单次 `Run` 内的重试；
  重试耗尽的条目保持 `failed`，再次调用 `Plan.Run` 即可续跑（幂等：已成功
  和已转人工的条目会被跳过）。
- **可恢复快照**：`Plan.Snapshot()` 序列化条目顺序、状态、尝试次数与人工项；
  `RestorePlan` 通过 `HandlerRegistry` 重新挂载补偿函数，跨进程重启后续跑。
- **人工处理项**：声明 `Compensatable: false` 或缺少补偿 handler 的步骤进入
  `ManualItems()`，计划状态为 `manual_required`，**绝不会**被报告为
  `rolled_back`。反复失败的条目也可由运维通过 `Plan.MarkManual` 转人工。

## 用法

```go
g := compensation.NewGraph()
_ = g.Add(compensation.Step{ID: "charge", Compensatable: true, Run: charge, Compensate: refund})
_ = g.Add(compensation.Step{ID: "ship", DependsOn: []string{"charge"}, Compensatable: true, Run: ship, Compensate: cancelShipment})
_ = g.Add(compensation.Step{ID: "notify", DependsOn: []string{"charge"}, Compensatable: false, Run: notify})

ex := compensation.NewExecutor(g, compensation.WithRetryPolicy(compensation.RetryPolicy{
    MaxAttempts: 3, Backoff: time.Second,
}))
res := ex.Execute(ctx)
if res.RunErr != nil {
    plan := res.Plan
    if err := plan.Run(ctx); err != nil {
        data, _ := plan.Snapshot() // 持久化，稍后/重启后 RestorePlan 续跑
        _ = data
    }
    switch plan.Status() {
    case compensation.PlanRolledBack:     // 全部补偿成功
    case compensation.PlanManualRequired: // 存在人工处理项，见 plan.ManualItems()
    case compensation.PlanFailed:         // 有失败条目，可再次 plan.Run 重试
    }
}
```

## 测试

```sh
go test ./...
```
