# Go 模块准入包

这个包为 Go 构建宿主提供模块发布准入，而不是简单比较不同代理的
`go.sum`。它验证可核对的 Merkle 日志、检查点一致性、见证者门槛和模块叶
包含关系，并把缺失证据转换为最小费用的补证计划。

## 核心规则

- 哈希全部使用 SHA-256。叶节点为 `SHA256(0x00 || payload)`，内节点为
  `SHA256(0x01 || left || right)`。
- 非满树在小于长度的最大二次幂处分割。空树根为 `SHA256("")`。
- 检查点由 `(size, root)` 共同标识，绝不只比较根或只比较大小。
- 见证消息绑定来源域标签、树大小和根摘要；同一实际管理域的不同别名只
  计一次票。
- 客户端永久保留已接受的检查点集合。更大的树不会覆盖旧根；同大小异根
  会报告 `observed_fork`。
- 新模块需要从每个已提交检查点到同一个目标检查点的有效一致性证据链，
  以及该目标上的模块叶包含证明。中间证据可乱序到达；缺证返回 `unknown`。
- 候选补证请求只用于生成费用计划。尚未取得的证明和签名不会被当成已经
  有效的证据。

## 快速使用

```go
module := admission.Module{
    Path: "example.com/dependency",
    Version: "v1.2.3",
    ContentDigest: digest,
}

tree := admission.NewTree(leaves)
target := admission.NewCheckpoint(uint64(len(leaves)), tree.Root())
inclusion, _ := tree.InclusionProof(moduleIndex)
consistency, _ := tree.ConsistencyProof(oldSize, uint64(len(leaves)))

pub := admission.Publication{
    Module: module,
    Target: target,
    LeafIndex: moduleIndex,
    InclusionProof: inclusion,
}

edge := admission.NewEvidenceEdge(oldCheckpoint, target, consistency, witnesses)
evaluator := admission.NewEvaluator(committedCheckpoints,
    admission.Policy{Threshold: 2, SignatureVerifier: verify})
result := evaluator.Evaluate(pub, []admission.EvidenceEdge{edge})

switch result.Status {
case admission.StatusAdmitted:
    // result 已经具备全部准入证据
case admission.StatusUnknown:
    // result.Plan 是条件式最小补证计划
case admission.StatusFork:
    // result.Fork 记录了同大小异根的检查点
}
```

持久化客户端使用原子临时文件加 `rename` 提交。`Client.Admit` 只有在磁盘
提交成功后才更新内存状态，因此提交失败会保留上一份信任状态。

```go
client, err := admission.NewFileClient("/var/lib/build-host/trust.json", 2)
result, err := client.Admit(pub, edges)
```

## 补证计划

`ProofOffer` 表示一个提供者能以固定费用获取的一组一致性边。计划使用精确
子集搜索，遵守 `PlanLimits.MaxAcquisitionsPerProvider`，并允许一条共享边
同时帮助多个旧根抵达目标。计划是条件式的：真正准入前仍须获取并验证每条
边的证明和签名。

## 测试

```sh
go test ./...
```

测试覆盖：

- 1 到 17 个叶子的所有包含证明和一致性证明组合，包括非满树；
- 缩短/带冗余旧根的一致性证明；
- 同一实际域重复签名与别名去重、跨树重放；
- 乱序证据、缺失中间边、同大小异根；
- 共享证明边、按提供者限额的最小费用计划与全子集费用核对；
- 原子提交失败时保留旧信任状态。
