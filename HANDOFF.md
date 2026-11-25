# 部分地址段转交准入

根目录就是可导入的 Go 包，包名为 `handoff`，导入路径为
`prescreen.local/ddd-10562-v3`。包只使用内存结构和 Go 标准库，不访问磁盘或网络。

## 安全模型

- `Spec` 描述小地址域、出口集合、新旧租户、转交前缀、必须保持可达的关键前缀、偏序和每出口乱序窗口。
- `Statement` 是一次原子发布/撤回批次；多个前缀可合并在同一个确切 SHA-256 摘要中。
- `Receipt` 由出口 Ed25519 私钥签名，绑定出口、计划、`Statement.Digest()`、代次、出口本地版本和 nonce。
- `View` 是单个出口自己的可见版本。`Select` 先做最长前缀匹配；同长前缀出现不同租户时返回 `OutcomeConflict`，绝不转发。
- 默认准入策略会剔除未授权的更长候选，再在剩余候选中做最长匹配。这等价于先过滤未授权路由、再安装 RIB。

在异步出口和全局授权切点同时存在时，如果把“原始 LPM 赢家”直接装入转发面，任何消息协议都无法同时保证原始赢家在每个中间时刻均已授权且关键前缀不中断：新租户更具体前缀先到会提前吸引流量，切点后旧前缀残留则会误投给旧租户。因此证书证明的是准入后的 `Forward` 决策与实际授权一致；同时记录所有原始 LPM 偏差，并验证这些偏差只导致跳过、拒绝或冲突，而不会产生未授权 `Forward`。

## 在线转交步骤

`BuildSafePlan` 生成以下屏障：

1. 如果旧声明与新声明同长且前缀相同，先发布精确撤回语句。
2. 等相关出口各自对撤回语句的确切摘要返回有效 ACK；一个出口的 ACK 不能用于另一个出口。
3. 预置新租户前缀。它可以提前到达，但切点前未授权；准入层跳过该更长候选，继续使用仍授权的旧聚合或更短覆盖路由。
4. 等所有出口对合并后的 `preposition-new` 确切摘要返回当前代次 ACK。
5. 才允许执行授权切点：撤销旧租户在转交段上的授权，并授予新租户。

`Controller.NextBatch` 根据 `ReceiptBook` 中已观测、验签且非重放的 ACK 在线返回当前批次；缺少的确认在 `Batch.Missing` / `Controller.MissingAcks` 中给出。

## 偏序与乱序窗口

`Protocol.Precedence` 保存任意偏序边。`Spec.Window` 是每出口有界乱序窗口：

- `Window=1`：严格按该出口的预期投递顺序。
- `Window=2`：允许后一条消息超过前一条一次。
- `Window=W`：只有序号小于“当前最小未投递序号 + W”的消息可成为下一步。

`Protocol.EnumerateTraces` 和证书签发器会 DFS 所有满足偏序与窗口的完整交错，并检查每个交错的每个前缀状态。

## 独立证书

`IssueCertificate` 会：

1. 从所有宣告前缀、排除洞、授权范围、切点操作和关键前缀构造路由/授权等价类。
2. 穷举所有合法交错和中间版本。
3. 在每个状态对每个出口、每个等价类执行 `Select`。
4. 校验所有 `Forward` 都有实际授权，且关键类始终得到唯一授权 `Forward`。
5. 对所有 `(交错, 状态, 出口, 等价类)` 证据计算排序后的 SHA-256 `Root`。
6. 输出 `Violations` 和 `RequiredRecoveryAcks`，说明还需要哪个出口对哪个确切摘要补 ACK。

宿主可把 `Certificate` 编码为 JSON 存档。`VerifyCertificate` 不信任证书中的 `Safe` 布尔值，而是用同一 `Protocol` 和验证选项独立重放，并比对协议摘要、根、计数和首个反例。

## 小地址域对照

内置测试使用 `10.10.0.0/24`：

- 原聚合：`10.10.0.0/24` 属于 `tenant-a`。
- 仅转交：`10.10.0.128/25` 给 `tenant-b`。
- 关键前缀：`10.10.0.0/26` 和整个转交半段 `10.10.0.128/25`。
- 3 个出口，窗口为 2。
- 安全计划有 90 个合法完整交错。
- 测试逐交错、逐中间状态、逐出口、逐 256 个地址核对；共执行 552,960 次地址级决策对照。

运行：

```sh
go test -v ./...
go vet ./...
go test -race ./...
```

关键测试：

- `TestSafePlanEveryAddressAndInterleaving`：部分聚合移交的逐地址/逐交错安全与可达性。
- `TestNaivePlanFinalCorrectButIntermediateMisdirects`：无 ACK 屏障计划最终正确，但证书找到中间原始 LPM 误投/切点后不可达反例。
- `TestEqualLengthConflictRequiresWithdrawalAck`：同长异租户冲突及恢复所需撤回 ACK。
- `TestReceiptReplayAndGenerationsAreEgressBound`：重放、旧代次、nonce 和出口绑定。
- `TestMergedPrefixDigestAndEgressLocalAck`：合并前缀摘要不同于拆分摘要，ACK 只对签名出口有效。
- `TestOverlappingAnnouncementsAndExclusionHoles`：重叠宣告和排除洞。
- `TestCriticalPrefixesPreventBlanketWithdraw`：关键前缀阻止“全部停发”式假安全。

## 示例

```go
spec := handoff.Spec{
    PlanID:    "partial-handoff",
    Domain:    handoff.MustPrefix("10.10.0.0/24"),
    Egresses:  []handoff.EgressID{"e1", "e2"},
    OldTenant: "tenant-a",
    NewTenant: "tenant-b",
    Transfer:  []netip.Prefix{handoff.MustPrefix("10.10.0.128/25")},
    Critical:  []netip.Prefix{handoff.MustPrefix("10.10.0.128/25")},
    InitialClaims: []handoff.Claim{{
        Prefix: handoff.MustPrefix("10.10.0.0/24"),
        Tenant: "tenant-a",
    }},
    InitialAuthz: handoff.NewAuthorization().
        Grant("tenant-a", handoff.MustPrefix("10.10.0.0/24")),
    Window: 2,
}

plan, _ := handoff.BuildSafePlan(spec)
cert, _ := handoff.IssueCertificate(plan.Protocol, handoff.VerifyOptions{})
if err := cert.RequireSafe(); err != nil {
    // cert.Violations / cert.RequiredRecoveryAcks explain recovery.
}
verified, err := handoff.VerifyCertificate(cert, plan.Protocol, handoff.VerifyOptions{})
_ = verified
_ = err
```
