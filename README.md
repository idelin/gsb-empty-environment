# 云事件身份序列关联

云审计里删除凭据与使用凭据可能乱序上报，不能直接按到达顺序判定违规。本包为 Go
检测宿主实现时间区间关联：每个事件携带可信提交序号（`Feed`/`Seq`）与可能发生时间
区间（`Lo`/`Hi`，离散整数时间域，闭区间）。

## 处理流程

1. **序号约束收窄区间**：同一 feed 内按序号建立严格偏序约束
   `t_前驱 + 1 <= t_后继`，构成差分约束 DAG。一次正向拓扑传播收紧下界、一次反向
   传播收紧上界，得到每个事件精确的可行区间；区间塌缩（`Lo > Hi`）记为
   `ConflictInconsistentWindow` 但事件保留。
2. **跨撤销边界判定**：对同一凭据的每个「使用 / 撤销」事件对给出结论：
   - `VerdictBefore`：使用必然严格早于撤销；
   - `VerdictAfter`：使用必然严格晚于撤销（撤销后仍在用，疑似违规）；
   - `VerdictStraddle`：两种次序（含同时发生）都可能，需人工复核；
   - `VerdictUnknown`：输入区间互相矛盾，无可行时间。

   序号 DAG 中可达的事件对直接按可信路径判定；不可达的事件对依据收窄后的区间
   判定，并单独计算同时发生的可行性。
3. **小时间域枚举核对**：当凭据分组的事件数与候选赋值量在预算内时，穷举所有满足
   约束的时间赋值，独立统计每对事件先于/晚于/同时的赋值数并复核结论。分析结论与
   枚举不一致时输出 `ConflictCheckMismatch`，两份结论同时保留，绝不静默取舍。

冲突事件（塌缩区间、同 feed 重复序号、枚举不一致）与歧义事件对都会原样保留在
`Report` 中。

## 用法

```go
events := []netsec.Event{
    {ID: "use-1", Feed: "audit-a", Seq: 2, Kind: netsec.KindUse,
        Cred: "AKIA123", Lo: 10, Hi: 20},
    {ID: "rev-1", Feed: "audit-b", Seq: 1, Kind: netsec.KindRevoke,
        Cred: "AKIA123", Lo: 15, Hi: 25},
}

report, err := netsec.Correlate(events)
if err != nil {
    log.Fatal(err)
}
for _, p := range report.Pairs {
    fmt.Printf("%s vs %s: %s (枚举核对=%v)\n",
        p.UseID, p.RevokeID, p.Verdict, p.EnumChecked)
}
for _, c := range report.Conflicts {
    fmt.Printf("冲突: %s %s %s\n", c.Kind, c.EventID, c.Detail)
}
```

枚举预算可通过 `netsec.WithMaxEnumNodes` 与
`netsec.WithMaxEnumAssignments` 调整；超预算时跳过穷举，分析结论照常输出，
`PairResult.EnumSkipped` 置位。

## 设计要点

- 只信任同 feed 的提交序号；跨 feed 无序号关系，完全由区间可行性决定。
- 时间为整数 ticks，严格先后意味着至少相差 1；边界算术做了 int64 溢出饱和处理。
- 穷举采用拓扑序回溯，边约束随赋值即时剪枝；测试中另有独立实现的暴力枚举对照。
- 全部逻辑无外部依赖，`go test ./...` 覆盖核心分支（96%+ 语句覆盖）。
