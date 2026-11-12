# 有认证的删节日志

审计宿主从原始日志产生可删节导出，并凭独立获得的 `(LogID, Root)` 信任锚验证导出。隐藏记录只暴露承诺；公开记录暴露原文、载荷摘要和 Merkle 成员路径。证明根同时绑定日志 ID 与记录总数，因此删除、重排、重复序号或跨日志拼接都会导致验证失败。

## 用法

```go
log, err := netsec.NewLogFromPayloads("audit-2026-11", [][]byte{
    []byte("public event"),
    []byte("private event"),
    []byte("public event 2"),
})

// 只公开第 1、3 条（从 0 开始计数），其余记录仅含承诺。
export, err := log.Export([]int{0, 2})

// 信任锚应由宿主从审计日志发布通道单独获得。
verified, err := netsec.Verify(log.Anchor(), export)
```

`NewLog` 也接受显式序号；序号必须为正且严格递增，重复序号会被拒绝。`ExportAll` 与 `ExportNone` 分别导出全公开和全隐藏视图。`MarshalExport` / `UnmarshalExport` / `VerifyJSON` 用于 JSON 交换，解码会拒绝未知字段和尾随数据。

## 承诺结构

- 载荷摘要：`SHA-256(payload)`。
- 叶子承诺：`SHA-256(domain || logID || position || sequence || payloadDigest)`，字段均做长度前缀编码。
- 内部节点：`SHA-256(nodeDomain || leftCommitment || rightCommitment)`。
- 证明根：`SHA-256(rootDomain || logID || count || merkleRoot)`。

叶子显式绑定位置、序号和载荷摘要。公开项的验证会重新计算叶子并校验成员路径；隐藏项直接以其承诺填入对应位置。最后验证器用全部 N 个位置的叶子重建 Merkle 根并对照信任锚，因此任何遗漏或隐藏位置缺失都无法通过。非 2 的幂的树用固定空叶子补齐，而总数已由证明根绑定，补齐项不会被误认成记录。

## 验证拒绝的情形

- 导出日志 ID、根或记录总数与信任锚不一致；
- 条目缺失、额外、位置不连续或声明位置与槽位不符；
- 公开原文、摘要、承诺、序号或成员路径被修改；
- 公开条目出现重复序号；
- 隐藏条目夹带原文、摘要、序号或路径；
- 从另一日志搬入公开条目或隐藏承诺。

## 测试

```sh
go test ./...
go test -race ./...
```

测试穷举 9 条记录的全部 512 个公开子集，并分别覆盖遗漏、重排、重复序号、跨日志拼接、载荷/路径篡改和 JSON 严格解码。

审计导出需要隐藏部分记录内容，同时证明记录顺序及被隐藏位置没有被删掉。写供Go审计宿主调用的删节证明包，原日志每项承诺绑定序号与载荷摘要，导出对隐去项仅给承诺，对公开项给原文和成员路径；证明根绑定总条数。用任意公开子集测试遗漏、重排、重复序号和跨日志拼接均无法通过。
