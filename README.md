# replayhandoff

可导入的 Go 重放状态交接包。模块路径为
`prescreen.local/ps-bb6414b561`，包名为 `replayhandoff`。

该包只处理题内有限协议，不访问网络，也不枚举 40 位序号空间。迁移判定、
范围补页和覆盖证书都由半开区间代数完成。

## 核心不变量

- 序号是 `uint64` 中有效的 40 位值：`[0, 2^40)`；`2^40` 可以且只能作为
  半开区间的上端。
- `Partition{Context, Sender}` 是唯一的重放键。不同安全上下文或发送者之间
  不共享、不复用已见或未交付证明。
- `IntervalSet` 是不可变的规范化区间集合。快照可被多个节点视图共享，任何
  修改都返回新值，因此旧快照不会被后续提交改写。
- `NodeLog` 是同一节点、同一安全上下文中连续代次的签名批次链。批次头绑定
  前根、安全上下文摘要和代次；提交是幂等的，已承诺交付只记录一次。
- `RangeProof` 只对 `Range` 内的已见集合作证。范围之外的序号一律未知，不能
  当作空集。
- 未关闭或缺失的租约使判定保持 `wait`。未交付证明的屏障必须不早于租约关闭
  屏障；更早的证明只能用于“已见即拒绝”，不能据此接受。

## 快速使用

```go
package main

import (
    "crypto/ed25519"
    "fmt"

    replay "prescreen.local/ps-bb6414b561"
)

func main() {
    var seedA, seedB [ed25519.SeedSize]byte
    seedA[0], seedB[0] = 1, 2

    signerA, _ := replay.NewSeedSigner("old-a", seedA)
    signerB, _ := replay.NewSeedSigner("old-b", seedB)
    keys := replay.KeyRing{
        "old-a": signerA.Public,
        "old-b": signerB.Public,
    }

    p := replay.NewPartition("oscore-context", "sender")
    logA, _ := replay.NewNodeLog("old-a", "oscore-context", signerA)
    logB, _ := replay.NewNodeLog("old-b", "oscore-context", signerB)

    // A 已向业务交付 7，B 尚未交付。
    _, _ = logA.CommitInterval(p, replay.Interval{Lo: 7, Hi: 8}, signerA)

    proofA, _ := logA.Proof(p, replay.Interval{Lo: 0, Hi: 16}, signerA)
    proofB, _ := logB.Proof(p, replay.Interval{Lo: 0, Hi: 16}, signerB)

    engine, _ := replay.NewHandoff(replay.HandoffConfig{
        Nodes:   []replay.NodeID{"old-a", "old-b"},
        NewNode: "new",
        Leases: []replay.Lease{
            {Node: "old-a", Closed: true, Barrier: proofA.Barrier},
            {Node: "old-b", Closed: true, Barrier: proofB.Barrier},
        },
        KnownProofs: []replay.RangeProof{proofA, proofB},
        Keys:        keys,
    })

    verdict, _ := engine.EvaluateSequence(p, 7)
    fmt.Println(verdict.Decision) // reject：任一节点已见

    verdict, _ = engine.EvaluateSequence(p, 6)
    fmt.Println(verdict.Decision) // accept：所有可能节点都证明未见
}
```

## 批量查询与补页

`AnalyzeBatch` 接受任意顺序、可重叠的半开查询段，并返回：

- 每段的当前聚合决策；
- 段的逐原子区间决策及其证明见证；
- 获取补充页面后的逐区间决策；
- 全局最小成本的补充页集合；
- 可独立重算的覆盖证书。

```go
report, err := engine.AnalyzeBatch([]replay.QuerySegment{
    {Partition: p, Range: replay.Interval{Lo: 0, Hi: 32}},
})
if err != nil {
    panic(err)
}
for _, segment := range report.Segments {
    fmt.Println(segment.Index, segment.ProjectedDecision)
    for _, part := range segment.ProjectedParts {
        fmt.Println(part.Interval, part.Decision, part.Reason)
    }
}
fmt.Println(report.Supplement, report.SupplementCost)
```

候选页上限是 `MaxCandidatePages == 20`。比较前会：

- 校验每个证明的签名和范围；
- 对完全相同证明去重；
- 按分区/节点/屏障/范围稳定排序，输入乱序不影响结果；
- 对语义相同的页去重，保留成本较低的页；
- 同一节点、同一分区、同一屏障出现不同根时保留为隔离分支并报告
  `RootConflict`，绝不把不同根的 `Seen` 集合合并。

补页选择不使用序号枚举。未决区间先按所有证明与已见集合端点切成原子区间，
再对至多 20 个页做精确的加权子集搜索。选择任何一页若能看到某区间，可将该
区间判为拒绝；否则需要每个缺失节点都有同一页集合提供的新鲜未见证明。

## 覆盖证书

`CoverageCertificate` 自带有限输入、候选页、已选页、逐区间决策、见证摘要、
规范化信息和根冲突信息。它由摘要绑定，可选地由新节点 Ed25519 签名。
`CoverageCertificate`、`RangeProof` 和区间类型可直接 `json.Marshal` /
`json.Unmarshal`，结构体键控的路由/本地状态表会转换为稳定排序的数组。

```go
cert := report.Certificate
if err := replay.VerifyCertificate(cert, keys); err != nil {
    panic(err)
}
if err := replay.VerifyCertificateAuthenticated(cert, keys); err != nil {
    panic(err)
}
```

`VerifyCertificate` 会从证书内嵌输入重新执行完整分析，核对摘要、最小补页集、
成本、逐区间决策和每个见证证明；`VerifyCertificateAuthenticated` 还要求
发行者签名。

小域审计可同时枚举序号和所有页子集：

```go
audit, err := replay.VerifyCertificateExhaustive(cert, keys, 64)
if err != nil {
    panic(err)
}
fmt.Println(audit.SequencesChecked, audit.PageSubsets, audit.MinimumCost)
```

该函数只用于测试/审计小域，并会拒绝过大的穷举运行。生产判定始终使用区间
算法。

## 边界与错误

- `NewInterval(5, 0)` 返回 `ErrInvertedInterval`。
- `ParseInterval("[5,)")` 或 `ParseInterval("[,9)")` 返回
  `ErrMissingEndpoint`。
- 高于 `2^40` 的端点返回 `ErrOutsideSequenceSpace`；
  `EvaluateSequence(p, 2^40)` 同样拒绝。
- `[2^40-1, 2^40)` 是合法区间，上端不包含在集合中。
- 超过 20 个不同候选页在规范化后返回错误。
- 缺失租约记录按未关闭处理；缺少某个可能节点的未见证明时返回 `wait`，
  并在单序号判定中给出最小补页建议。

## 验证

```sh
go test ./...
go vet ./...
```

测试覆盖写时复制、链式批次、证明篡改、租约未结束、陈旧证明、范围缺端、
2^40 边界、跨安全上下文隔离、同屏障异根、跨节点交付竞态、重复/乱序页，
以及 20 组随机小域序号全枚举与所有候选页子集的最小成本核对。
