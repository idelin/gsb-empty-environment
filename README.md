# compensation

依赖图步骤执行器 + 可恢复的补偿（回滚）计划，Go 实现。

## 行为

- `Executor.Execute` 按拓扑层级正向执行步骤，同层步骤并行。
- 任一步骤失败时：不再调度新层级，等待在途步骤结束，然后对**所有已完成步骤**
  （失败步骤的祖先以及并行分支）生成补偿计划。
- 补偿按依赖逆序执行（被依赖者最后补偿）。
- 计划可通过 `PlanPath` 持久化（每次状态迁移后原子写盘），崩溃后用
  `Resume` 从磁盘恢复；已完成的补偿不会重复执行。
- 补偿失败可重试（`MaxCompensateAttempts`）；重试耗尽或没有补偿处理函数的
  步骤会成为**人工处理项**（`RollbackReport.Manual`），
  `RollbackReport.Success()` 此时返回 false —— 不会把人工项报告为回滚成功。

## 示例

```go
steps := []compensation.Step{
    {ID: "create-order", Run: createOrder, Compensate: cancelOrder},
    {ID: "charge", DependsOn: []string{"create-order"}, Run: charge, Compensate: refund},
    {ID: "ship", DependsOn: []string{"charge"}, Run: ship}, // 无补偿 → 人工处理
}
ex := &compensation.Executor{MaxCompensateAttempts: 3, PlanPath: "rollback-plan.json"}
res, err := ex.Execute(ctx, steps)
if err != nil {
    // 图校验错误（环、未知依赖、重复 ID 等）
}
if res.Report != nil && !res.Report.Success() {
    for _, m := range res.Report.Manual {
        log.Printf("manual handling required: %s (%s)", m.StepID, m.Reason)
    }
}

// 崩溃恢复 / 重试失败的补偿：
plan, report, err := compensation.Resume(ctx, "rollback-plan.json", registry, 3)
```

## 测试

```sh
go test -race ./...
```
