TLS 与双向认证（mTLS）客户端在验证服务端证书链时，若按简化实现只做线性签名回溯，会在跨签名桥接 CA、名称约束与路径长度限制上出现严重安全放行漏洞或大面积误拒：1）**多路径图构建与跨签名回溯**：给定受信任根集合（Trust Anchors）、非信任中间池（Intermediates）与终端叶证书，一个中间 CA 可能存在多个同 Subject 不同 Issuer 的跨签名证书（例如旧根已过期但新根仍有效！）——路径构建器必须基于 `(Subject, AuthorityKeyID -> SubjectKeyID)` 执行有环检测的回溯搜索，当一条候选链因有效期、吊销、`pathLen` 或名称约束失败时继续探索其他可达信任锚的有效链！2）**RFC 5280 §6.1.4 `pathLenConstraint` 精确计数**：验证路径 `[CA_0(Anchor), CA_1, ..., CA_k, Leaf]` 时，每经过一个非自颁发（`Subject != Issuer`）中间 CA 才将剩余允许深度减 1，且**终端叶证书绝不消耗 `pathLenConstraint`**（`pathLenConstraint = 0` 的中间 CA 合法签发叶证书，但不可再签任何下级非自颁发中间 CA！），而自颁发中间 CA（密钥轮换过渡证书 `Subject == Issuer`）按规范**不消耗** `pathLenConstraint`！3）**`NameConstraints` 累积交并集与 SAN 压倒 CN**：沿证书链自顶向下维护 `permittedSubtrees`（与各层取交集！）与 `excludedSubtrees`（与各层取并集！），对 DNS（`.example.com` vs `example.com` 严格遵循 RFC 5280 子域边界！）、IP CIDR 与 Email 逐一校验叶证书的全部 SAN 项；一旦叶证书包含任何 DNS SAN，主机名匹配必须**只校验 SAN 并彻底忽略 Subject CN**；4）**`KeyUsage`/`ExtendedKeyUsage` 链式交集与未知关键扩展（Critical Extension）拦截**：中间 CA 必须具备 `CA=true` 与 `keyCertSign`；若中间 CA 携带非空 `EKU`，则叶证书目标用途（如 `ServerAuth`）必须同时被链上每个带 EKU 的中间 CA 允许（含 `AnyExtendedKeyUsage` 豁免！）；遇到无法识别且标记为 `Critical=true` 的扩展立刻拒绝该路径，同时结合 CRL（校验签名与时间窗、跳过已吊销序列号）完成终态判定。为传输安全控制面交付可导入的 Go RFC 5280 X.509 路径构建与严格验证引擎。验收覆盖跨签名过期主路径自动回溯至有效备用根、`pathLenConstraint=0` 放行叶证但拦截下级中间 CA、自颁发中间 CA 不耗 `pathLen`、多层 `permitted`/`excluded` NameConstraints 交并集、SAN 压倒 CN、中间 CA EKU 收紧与未知关键扩展拦截。

---

## 交付物：`pathverify` 包

模块路径：`prescreen.local/ps-371913a14a/pathverify`（纯标准库实现，无第三方依赖）。

### 架构：构建与验证分离

引擎刻意把「路径构建」和「策略验证」拆成两个阶段，避免线性回溯导致的误放行/误拒：

1. **多路径图构建**（`builder.go`）：以 `(Subject, SubjectKeyID)` 建索引，
   按子证书的 `AuthorityKeyID`（缺失时退化为 Subject DN）枚举候选签发者，
   自叶证书向上做带**环检测**的 DFS，仅用密码学验签判断边是否成立，
   枚举出所有可到达信任锚的候选链。跨签名桥接证书天然产生多条候选链。
2. **逐候选链严格验证**（`verify.go`、`nameconstraints.go`、`crl.go`）：
   对每条候选链执行完整 RFC 5280 策略；任一链失败即记录诊断并继续下一条，
   第一条通过全部检查的链作为结果返回；全失败时返回带 `Attempts` 的结构化错误。

### 实现的安全语义

- **`pathLenConstraint`（RFC 5280 §6.1.4 (l)）**：仅非自颁发（`Subject != Issuer`）
  中间 CA 消耗深度；自颁发过渡证书（`Subject == Issuer`）不消耗；叶证书绝不消耗，
  因此 `pathLen=0` 的 CA 可直接签叶证但不能再签下级非自颁发中间 CA。
- **NameConstraints**：`permittedSubtrees` 逐层取交集、`excludedSubtrees` 逐层取并集；
  覆盖 DNS（`.example.com` 仅匹配子域、不匹配 `example.com` 自身）、IP CIDR、
  Email、URI，逐一校验叶证书的**全部** SAN。
- **SAN 压倒 CN**：叶证书含任意 DNS SAN 时主机名只匹配 SAN 并彻底忽略 Subject CN；
  完全无 SAN 时才回退到 CN；支持最左标签通配符（恰好匹配一个标签）。
- **KeyUsage/EKU**：签发者必须 `CA=true`，携带 KeyUsage 时必须含 `keyCertSign`；
  目标 EKU 须同时被链上每个带非空 EKU 的证书放行，`AnyExtendedKeyUsage` 豁免。
- **未知关键扩展**：`UnhandledCriticalExtensions` 非空立即拒绝该路径。
- **CRL**：校验 CRL 自身签名与 `ThisUpdate/NextUpdate` 时间窗，命中吊销序列号即拒绝，
  并可借此回溯到其他有效候选链。
- **信任锚有效期**：默认严格校验（拦截「旧根已过期」的跨签主路径），
  可用 `IgnoreAnchorValidity` 切换到纯 RFC 5280（不强制锚点有效期）行为。

### 用法

```go
import "prescreen.local/ps-371913a14a/pathverify"

result, err := pathverify.Verify(pathverify.Options{
    Leaf:          leafCert,
    Roots:         trustAnchors,       // *pathverify.CertPool
    Intermediates: intermediates,      // *pathverify.CertPool，可为空
    EKU:           []pathverify.ExtKeyUsage{pathverify.ExtKeyUsageServerAuth},
    DNSName:       "service.example.com",
    CurrentTime:   pathverify.AtTime(now),
    CRLs:          crlList,             // []*x509.RevocationList，可为空
})
```

失败时错误为 `*pathverify.Error`，`Reason` 取 `Reason*` 常量（如 `ReasonPathLen`、
`ReasonNameConstraint`、`ReasonRevoked`），支持 `errors.Is` 按原因匹配，
`Attempts` 保留每条候选链的失败细节。证书池支持 `AddCert`/`AddDER`/`AddPEM`。

### 验收测试

`go test ./...` 全绿（含 `-race`），覆盖全部验收点：

| 验收点 | 测试 |
| --- | --- |
| 跨签名过期主路径自动回溯至有效备用根 | `TestCrossSignBacktrack` |
| 吊销一条跨签链后回退到另一条 | `TestCrossSignRevocationFallback` |
| `pathLen=0` 放行叶证、拦截下级中间 CA | `TestPathLenZero` |
| 自颁发中间 CA 不耗 `pathLen`（含对照组） | `TestSelfIssuedDoesNotConsumePathLen` |
| 多层 permitted 交集 / excluded 并集、DNS 边界 | `TestNameConstraints` |
| IP CIDR 与 Email 约束 | `TestNameConstraintsIPAndEmail` |
| SAN 压倒 CN、无 SAN 回退 CN、通配符 | `TestSANOverridesCN` / `TestCNFallbackWithoutSAN` / `TestWildcardHostname` |
| 中间 CA EKU 收紧、AnyEKU 豁免、mTLS ClientAuth | `TestIntermediateEKUTightening` / `TestClientAuthEKU` |
| CA 缺 keyCertSign、叶子 KeyUsage 拦截 | `TestCAKeyCertSignRequired` |
| 未知 Critical 扩展拦截 | `TestUnknownCriticalExtension` |
| CRL 验签/时间窗/吊销终态 | `TestCRLRevocation` |
| 环检测安全终止、无可达锚点 | `TestCycleDetection` / `TestNoPathToAnchor` |
