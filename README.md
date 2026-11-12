# 多步骤授权见证完整性

敏感操作需要先确认设备再确认用户，两个独立成功事件不能随意组合。写供Go接入宿主调用的见证组合包，步骤证明绑定操作摘要、会话、父证明摘要和有效时间交集；完成必须沿同一链覆盖指定步骤图。支持并行步骤但不允许拼接不同祖先，用真实摘要和伪验签器测试缺步、重放及分支合并。

## 包 `netsec`

纯标准库实现的多步骤授权见证组合包，宿主通过 `NewValidator(verifier, replayCache)`
接入后调用 `Validator.Complete` 判定一次敏感操作是否被完整、合法地见证。

### 核心类型

- `Proof`：单步见证，签名内容固定绑定 `OpDigest`（操作摘要）、`SessionID`（会话）、
  `ParentDigest`（父证明摘要，根证明为 `ZeroDigest`）、`NotBefore/NotAfter`（有效时间窗）
  以及 `StepID`、`SignerID`。
- `Proof.Digest()`：对规范化 JSON（时间取 UTC Unix 秒）计算真实 SHA-256 摘要，
  摘要不含签名字段，签名由 `Verifier` 覆盖同样的规范化字节。
- `StepGraph`：必须覆盖的步骤与有向边（前置关系），`Validate` 用 Kahn 算法拒绝环和悬空引用。
- `Bundle`：宿主提交的证明集合，无需预排序，验证器按父摘要重建链。
- `Completion`：成功结果，返回覆盖步骤、链序证明摘要和所有时间窗的交集。

### 完成判定（全部通过才算成功）

1. 图是合法 DAG，证明字段与各自时间窗合法，且证明摘要在包内不重复。
2. 所有证明绑定同一个操作摘要和同一个会话，否则分别返回 `ErrOpMismatch`/`ErrSessionMismatch`。
3. 每个证明通过宿主 `Verifier` 验签（失败返回 `ErrBadSignature`）。
4. 证明集合恰好构成一条单链：唯一根、父摘要可解析、每个证明最多一个后继
   （`ErrForkedChain`）、无孤儿（`ErrOrphanProof`）、无多根（`ErrMultipleRoots`）。
5. 时间窗交集非空（`ErrNoWindowOverlap`），交集为“最晚 NotBefore ～ 最早 NotAfter”；
   传入非零 `at` 时还要求该时刻落在交集中（`ErrNotEffective`）。
6. 单链覆盖图的全部步骤、无未知/重复步骤，且每条边的前置步骤在链上更早
   （`ErrMissingStep`/`ErrUnknownStep`/`ErrDuplicateStep`/`ErrEdgeOrderViolated`）。
7. 配置 `ReplayCache` 时，链上摘要必须未被消费过（`ErrReplayedProof`）；
   仅在全部检查通过后才 `Mark`，失败绝不污染缓存。

并行步骤通过“DAG 约束 + 单链线性化”支持：同一根上的并行步骤可按任意拓扑序排列，
但不同祖先的证明无法拼接，任何分叉/合并都会在第 4 步被拒绝。

### 伪验签器与测试

`PseudoSigner`/`PseudoVerifier` 基于 HMAC-SHA256（常量时间比较），仅供测试与本地集成，
生产环境应由宿主提供基于设备/用户密钥的 `Verifier` 实现。

测试使用真实 SHA-256 摘要和伪验签器，覆盖：摘要随绑定字段变化、正常链、并行步骤的两种
线性化、缺步、未知/重复步骤、跨包重放与包内重复摘要、分叉父/双根/悬空父三类分支合并、
坏签名与错误密钥、操作/会话错配、篡改父摘要、时间窗不相交/未生效/非法窗、边序倒置、图环。

```go
validator := netsec.NewValidator(myVerifier, netsec.NewMemoryReplayCache())
completion, err := validator.Complete(ctx, graph, netsec.Bundle{Proofs: proofs}, time.Now())
```
