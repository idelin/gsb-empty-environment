# archiveproof

给 Go 档案宿主的固定连续性证书组件。验证方只读取“获准记录”和获准的共享承诺；不会在验证时回查隐藏记录，也不会因为实际校时不同而临时披露新的节点。

模块路径：`formal.local/w3-a2a139a7a9`，根目录即 `package archiveproof`，可直接 `import "formal.local/w3-a2a139a7a9"`。

## 数据模型

每条证据记录是 `Node`：

- `ID`、`Content`：记录身份与内容摘要。
- `Service`：服务永久身份；`Key` 只是轮换中的签名钥。
- `Source`：永久时钟来源；为空时取 `Service`。因此同一服务换钥仍共用一个时钟偏移变量。
- `Time`、`Offset`：记录时间及该时钟来源偏移的签名上下界。
- `Commits`：一个签名时间戳可同时承诺多个不透明证据（`CommitmentLink`）。
- `Renews`：续证边。指向可见记录时必须同时给出 `OldID` 和 `OldContent`，并与目标身份、内容摘要完全一致；指向隐藏记录时只可给 `OldCommitment`。
- `Approved`：验证方是否获准直接读取该记录。

`SharedEvidence` 表示一个或多个隐藏记录的共享承诺。同一共享承诺覆盖的记录共享时间戳、时钟来源、偏移上下界和续证边，正对应“大量早期记录共享时间戳与算法迁移凭据”的情况。`Covers` 是宿主侧的隐藏身份集合，**永远不会写入证书**。非空 `LicensedBy` 表示该承诺已被一个获准时间戳签名许可（`LicenseProof` 为宿主提供的不可解析证明）；若 `LicensedBy` 为空，但某个获准节点的 `Commits` 已签名承诺它，也会自动视为许可。

一个实际校时是 `Clocking`：`来源 -> 偏移`。同一来源在所有记录上只能有一个值；`Archive.ClockDomains` 对所有获准记录的偏移区间求交，交集为空则拒绝档案。

## 固定证书

`BuildCertificate` 返回 `Certificate`：

- `Entries` 只可能包含两种单元：
  - 完整披露的获准 `Node`；
  - 仅含承诺、共享元数据和许可证明的 `SharedEvidence`。
- `Genesis` 是原始签署端点，`Acceptance` 是当前验收端点。
- `Sources` 记录证书覆盖的相容校时域。
- 所有隐藏身份、隐藏内容、`Covers`、未选中的续证目标都会在选择披露时被剥除。

`Certificate.Verify(clocking)` 只使用证书内部节点，按实际偏移选择一条 `Acceptance -> Genesis` 的链，并返回每一级证据引用。`Certificate.VerifyAll` 枚举所有相容校时并逐一检查。

`Certificate.ValidateAgainst(archive)` 额外执行隐私边界检查：证书不得包含未获准节点身份、隐藏内容摘要、未许可承诺或宿主侧 `Covers` 元数据。

## 最小证书

目标函数是证书内不同证据单元数（一个共享承诺批次计一个单元），不是底层隐藏记录数。

构建流程：

1. 校验续证绑定、时钟域、许可关系与端点。
2. 建立获准节点与已许可共享承诺的候选图。
3. 静态剪枝：删去不可能出现在任何 `Acceptance -> Genesis` 路径上的记录。这使含数千条无关获准记录的档案仍可缩成小图。
4. 枚举相容校时。整数域足够小时逐点全枚举；域很大时使用所有签名断点、相邻整数与区间中点。
5. 对至多 `Options.MaxExactCandidates`（默认 24）个候选做迭代加深分支定界，得到**精确最小**证书；更大的图使用“各校时最短链并集 + 冗余删除”的确定性启发式。

因此小图有可复核的最优性保证；超大图保留线性/近线性的构建路径，但不承诺全局最优。调用方可通过 `Options` 调整精确阈值。

## 使用

```go
archive, err := archiveproof.LoadArchive("archive.json")
if err != nil {
    return err
}
cert, err := archiveproof.BuildCertificate(archive, nil)
if err != nil {
    return err
}

proof, err := cert.Verify(archiveproof.Clocking{
    "clock-a": 0,
    "clock-b": 7,
})
if err != nil {
    return err
}
for _, chain := range proof.Chains {
    for _, step := range chain.Path {
        // step.Node 或 step.Commitment；证书外不存在可临时读取的节点。
    }
}
```

命令行：

```sh
go run ./cmd/archivecert build -out cert.json archive.json
go run ./cmd/archivecert verify -clocking clock.json cert.json
go run ./cmd/archivecert verify-all cert.json
go run ./cmd/archivecert audit -clocking clock.json archive.json
```

## 测试覆盖

`go test ./...` 包含：

- 时间相接：续证差值必须落在签名区间内。
- 共享前驱：多个验收分支复用同一早期节点，证书只计一次。
- 服务换钥：`Service`/`Source` 相同、`Key` 不同仍共享一个偏移；同一永久身份下不相交的偏移直接拒绝。
- 不同校时：固定证书同时包含两个条件分支，实际校时决定 `Verify` 选择哪一条。
- 隐藏并集：不同校时各自有经隐藏节点的宿主侧安全链，但其并集含不可披露节点；证书改用一条获准的共享批次承诺，既不披露隐藏身份/内容，又保持最小单元数。
- 数千条记录共享一个早期承诺：证书只含一个批次承诺。
- 小图全枚举：逐个子集求最小证书，与构建结果逐例比较；另对四节点六条可能续证边的全部 `2^6` 子图做穷举。

## 边界说明

- 本组件验证结构、时间约束、续证绑定与披露边界；具体签名算法由宿主提供。`LicenseProof` 被当作不透明许可对象，不应内含隐藏内容。
- `Time`、偏移和差值均为 `int64` 秒级整数；溢出边界在构建和验证时拒绝。
- 共享承诺表示“同时间戳、同时钟来源、同偏移、同续证边”的一批隐藏记录。若隐藏记录间续证结构不同，应拆成不同共享承诺。
