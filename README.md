# 路由策略泄漏反例

边界路由器的导出规则可能把内部路由传给不该接收的邻居。为Go策略发布层写可导入的检查包，策略为条件分支、社区标记增删及接受拒绝，条件含前缀区间、路径长度和标记集合；规则可调用无递归子策略。给定机密标记与禁止邻居，求最小泄漏路由和逐条执行轨迹。限制十六种标记，用有限域穷举核对变换后条件与提前返回。

## 包结构

导入路径 `example.org/netsec/uid1292`，包名 `netsec`。

- `tags.go`：16 标记宇宙与 `TagSet` 位集合（域大小 2^16）。
- `prefix.go`：`netip` 前缀封装、CIDR 包含与 `PrefixRange`（网段 + 长度区间）。
- `conditions.go`：`PrefixRange`、`PathLenRange`、`TagCondition`（All/Any/None）及 `AndConds/OrConds/NotConds`。
- `policy.go`：`Rule`、`Policy`、`PolicySet`，动作 `AddTags/DelTags/Accept/Reject/CallPolicy`，判定与逐规则 `Trace`。
- `validate.go`：悬空引用、调用图递归（DFS 三色）、不可达规则等结构校验。
- `domain.go`：前缀地址/长度、路径长度的切点原子区间枚举与条件收集。
- `leak.go`：`CheckLeak` 有限域穷举，输出 `LeakReport`（最小 `LeakWitness` + 轨迹 + 域统计）。
- `trace.go`：`RenderTrace` 缩进渲染，含匹配/跳过/提前返回/子策略嵌套。

## 语义

- 规则按序执行；条件匹配后执行动作。`Accept/Reject` 立即终止并向上层传播，其余规则在轨迹中记为 `not-run (earlier verdict)`。
- `CallPolicy` 调用无递归子策略；子策略的终局判定立即传播，未终局则调用方继续。
- 标记增删作用于当前路由状态，后续条件基于变换后的集合判定。

## 有限域穷举

- 标记域：默认仅枚举策略实际引用的标记加机密标记（未引用标记不影响任何条件，结果等价）；`FullTagDomain` 可强制枚举全部 65536 个集合。
- 数值域：把所有 `PrefixRange/PathLenRange` 的边界转为切点，得到原子区间 `[lo, hi]`，区间内取值对所有条件结果等价，每区间取 `lo` 为代表，从而精确覆盖“变换后条件”。
- 泄漏定义：初始路由携带 `SecretTag` 且终局为 `ACCEPT`；`RequireSecretRetained` 可额外要求导出时仍带机密标记。
- 最小性：按前缀地址、前缀长度、路径长度、初始标记集合、邻居名的全序取最小见证。

## 快速使用

```go
ps := &netsec.PolicySet{Policies: map[string]*netsec.Policy{ /* ... */ }}
report := ps.CheckLeak(netsec.LeakSpec{
    Entry:      "export",
    SecretTag:  0,
    Neighbors:  []netsec.Neighbor{{Name: "forbidden-peer"}},
    MaxPathLen: 6,
})
if !report.Safe {
    fmt.Println(report.Leak.Route)
    fmt.Print(netsec.RenderTrace(report.Leak.Trace))
}
```

可运行示例见 `example_test.go` 的 `ExamplePolicySet_CheckLeak`。

## 验证

```sh
go test ./... -race -count=1
go vet ./...
```
