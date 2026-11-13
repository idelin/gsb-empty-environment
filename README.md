# 跨租户内存去重与写时延探针

`netsec` 是一个确定性的 Go 隔离宿主内存模型，用来研究页面去重（page
deduplication）造成的跨域写时复制（COW）侧信道。

## 模型

- 每个虚拟页包含 `DomainID`、内容摘要、敏感标签集合、显式 `Shareable`
  标志和物理页 ID。
- 宿主只把**摘要相同**的页面作为候选；最终是否合并由 `MergePolicy`
  回调决定。
- `ConservativePolicy`（默认）：必须同 domain、双方 `Shareable == true`
  且双方都没有敏感标签。跨 domain 默认禁止。
- `HardenedPolicy`：与默认策略相同的安全约束，用于验证跨 domain 观测轨迹
  不随受害秘密变化；同 domain 的干净可共享页面仍可去重。
- `GlobalDedupPolicy`：故意不安全的全局去重策略，忽略 domain、标签和
  shareable 标志，用于复现探针。
- 摘要比较就是页面身份比较。`PageSpec.Digest` 覆盖或自定义 `DigestFunc`
  可以模拟摘要碰撞替身：内容不同但摘要相同的页面会被合并。

### 成本

`CostModel` 为每种事件提供闭区间 `[Min, Max]`，支持噪声：

| 事件 | 默认成本 |
| --- | --- |
| 合并页首次写（COW） | `10` |
| 私有页写 | `1` |
| 读/Prime 等其他访问 | `0` |
| COW 失败（可注入） | `10` |

`CompareTraces` 只比较攻击者可观测的成本序列。若两个轨迹的某个成本区间
不相交，报告 `Leak`；若区间重叠，无法排除噪声使轨迹相同，只能报告
`PossibleLeak`。

### 生命周期

- `UpdateContent` / `UpdateTags` / `SetShareable` 在共享页上先解除合并，
  再执行写；解除合并本身产生一次 COW 事件。
- 物理页引用计数归零才回收。
- 虚拟页和物理页地址均会复用；`PageRef` 携带代次，旧引用返回
  `ErrStalePage`。
- `FailNextCOW` 或 `Config.COWFault` 可注入 COW 失败；失败不改变内容、
  映射或引用计数。

## 泄漏实验

`RunExperiment` 枚举有限候选秘密集合。对每个秘密建立受害页，再对每个
候选内容执行攻击者 `Prime -> Write` 序列：

1. 攻击者在自己的 domain 创建候选内容页并执行固定成本访问；
2. 首次写若命中受害页去重则支付 COW 成本，否则支付私有写成本；
3. 按完整观测轨迹对秘密分组；
4. 输出能区分两个秘密的最短序列、对应秘密对和确定性结论。

```go
report, err := netsec.RunExperiment(
    []string{"alpha", "beta"},
    netsec.ExperimentConfig{
        Policy:            netsec.GlobalDedupPolicy{},
        AttackerDomain:    "attacker",
        VictimDomain:      "victim",
        VictimShareable:   true,
        AttackerShareable: true,
    },
)
// report.Verdict == netsec.Leak
// report.ShortestSequence: prime("alpha"), write
```

跨 domain 使用 `HardenedPolicy` 时所有轨迹相同，报告 `NoLeak`。同 domain
的干净可共享页仍会泄漏；受害页带敏感标签或不可共享时不会合并。

`ExperimentConfig.Probes` 可传入自定义 `Sequence`；否则包会根据候选秘密
自动生成完整的 `Prime -> Write` 探针集合。对这一成本模型，长度 2 的序列
已经是完备搜索空间。

## 示例：直接观察 COW

```go
m := netsec.NewMemory()
a := m.CreatePage(netsec.PageSpec{DomainID: "d", Content: []byte("x"), Shareable: true})
b := m.CreatePage(netsec.PageSpec{DomainID: "d", Content: []byte("x"), Shareable: true})

// a 与 b 共用同一物理页；首次写分离并支付 10。
result, _ := m.Write(a, []byte("x!"))
// result.COW == true, result.Cost == netsec.FixedCost(10)
_ = b
```

运行测试：

```sh
go test ./...
```
