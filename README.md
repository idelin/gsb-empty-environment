# 分组统计披露控制组件

`example.org/netsec/q0491` 是可直接导入 Go 统计服务的披露控制层。统计服务仍然只处理分组汇总；引擎负责在结果离开服务前做最小计数、敏感属性抑制、差分预算记账、重复/交叉查询限制、舍入和噪声。

## 快速接入

```go
policy := netsec.DefaultPolicy()
policy.MinGroupSize = 5
policy.SensitiveMinGroupSize = 10
policy.SensitiveAttributes = map[string]bool{"diagnosis": true}
policy.TotalBudget = 1.0
policy.SubjectBudget = 1.0
policy.RoundingBase = 5
policy.NoiseMode = netsec.NoiseLaplace

engine, err := netsec.NewEngine(policy)
if err != nil {
    return err
}

result, err := engine.Execute(netsec.Query{
    ID:        "diagnosis-by-ward",
    Statistic: netsec.StatisticSum,
    Attribute: "diagnosis",
    Epsilon:   0.1,
    Groups: []netsec.GroupSummary{
        {
            Key:            "ward-a",
            Subjects:       subjectTokens, // >= SensitiveMinGroupSize 个去重主体令牌
            Value:          42,
            SensitiveCount: 11,
        },
    },
})
```

服务有记录级数据时，也可以让引擎生成汇总：

```go
dataset, err := netsec.NewDataset([]netsec.Record{
    {SubjectID: "s1", Group: "ward-a", Values: map[string]float64{"amount": 10}},
    {SubjectID: "s2", Group: "ward-a", Values: map[string]float64{"amount": 20}},
})
engine, err := netsec.NewEngine(policy, dataset)
result, err := engine.ExecuteDataset([]string{"ward-a"}, netsec.StatisticSum, "amount", 0.1)
```

`Subjects`/`SubjectIDs` 应使用服务端的不可逆主体令牌，而不是可公开关联的自然人标识。

## 控制规则

- 每组最小计数：按去重后的主体数判断；小于 `MinGroupSize` 的单元只返回抑制原因，不返回值。
- 敏感属性抑制：查询属性命中 `SensitiveAttributes`、设置 `Query.Sensitive`，或单元带敏感标记时，按 `SensitiveMinGroupSize` 执行更严格阈值；`SensitiveSuppressAll` 可无条件抑制。`SensitiveCount>0` 且小于阈值的小敏感子群也会被抑制。
- 差分预算：每次接受查询消耗 `Epsilon`；全局 `TotalBudget` 和逐主体 `SubjectBudget` 双重校验，任一超限整条查询拒绝，且不产生部分结果。
- 跨查询关联：主体级账本累计 epsilon 和查询次数；`MaxQueriesPerSubject` 限制同一主体反复出现。新查询与已接受查询的主体集合 containment 系数达到 `MaxQueryOverlap` 时视为子集/超集差分，直接拒绝。
- 重复查询：按“统计量 + 属性 + 分组键集合”生成指纹；完全重复的查询拒绝，不再返回可用于重复差分的额外结果。
- 结果处理：先按 `Sensitivity/Epsilon` 加入 Laplace 噪声（多单元查询按单主体最大参与单元数校准），再按 `RoundingBase` 量化。抑制查询同样消耗预算，因为“被抑制”本身也是受保护的输出信号。
- 无主体标识的纯计数汇总默认拒绝；只有显式开启 `AllowUnlinkedCells` 才允许，此时仍执行最小计数、敏感抑制、舍入、噪声和全局预算，但无法执行主体级累计。

`Execute` 返回错误时不释放任何分组，也不记账。成功的 `Result` 中每个单元都有 `Released` 标记；被抑制单元只带 `Reasons`。`Budget()` 和 `SubjectStatus()` 可用于监控剩余预算。

预算和主体关联账本驻留在 `Engine` 实例内。生产服务应为每个租户或隐私域复用同一个长期存活的引擎，不能按请求重新创建；否则跨查询累计会丢失。需要多实例部署时，应把账本快照持久化到受控存储后再水平扩展。

## 验证

`disclosure_test.go` 覆盖了极小分组、敏感属性与敏感子群、超集/子集差分、重复查询、按主体累计、主体查询次数上限、全局预算、舍入/噪声策略、纯计数汇总以及并发重复提交。运行：

```sh
go test ./... -race
```
