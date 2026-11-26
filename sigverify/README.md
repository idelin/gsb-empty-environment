# sigverify — 离线 Sigstore Bundle 验签与透明日志核验

`sigverify` 是一个**完全离线、零第三方依赖**的 Go 包，用于在软件制品安全准入
网关中核验 Sigstore 风格的签名 Bundle。它仅使用 Go 标准库密码原语，不发起任何
网络请求，也不依赖任何外部 Sigstore SDK。

## 解决的威胁

只校验制品签名而忽略透明日志证据时，攻击者可以：

- 伪造历史时间戳，使已过期泄漏的短期 Fulcio 证书看起来仍然有效；
- 用泄漏的短期密钥签发恶意镜像并声称是旧签名；
- 调包日志条目绑定的制品摘要或签名。

本包强制把签名、证书有效期与 Rekor 透明日志的可信时间锚定在一起。

## 验证流程

`Verify` 按以下顺序执行全链路检查，任一失败立即返回对应的哨兵错误：

1. 待验制品的 SHA-256 摘要必须等于 Bundle 声明的摘要；
2. 重新规范化日志条目（canonical JSON），并核对它绑定的摘要、ECDSA 签名、叶证书
   与 Bundle 完全一致，防止调包；
3. 解析并验证 Signed Checkpoint 的 Ed25519 签名与 origin；
4. 按 RFC 6962 计算叶哈希 `SHA-256(0x00 || canonicalEntryBytes)` 与内部节点哈希
   `SHA-256(0x01 || left || right)`，沿 `logIndex/treeSize` 重建 Merkle 根，与已
   验签 Checkpoint 根比对；拒绝 `logIndex >= treeSize`、路径长度不符、多余/缺失
   节点及非 32 字节哈希；
5. 可选 SET（Signed Entry Timestamp）用同一 Rekor 公钥验证；
6. 叶证书必须链接到可信 Fulcio 根，且显式携带 `CodeSigning` EKU；
7. 提取 OIDC 签发者（OID `1.3.6.1.4.1.57264.1.1` 与 `1.3.6.1.4.1.57264.1.8`）与
   SAN 身份（Email/URI），与调用方策略白名单精确匹配；
8. 以**可信日志时间** `integratedTime`（而非本机时钟）严格校验
   `NotBefore <= integratedTime <= NotAfter`（边界含等号）；
9. 用叶证书公钥验证制品的 ECDSA 签名。

## 信任锚与策略

调用方通过 `TrustedMaterial` 提供本地预置的 Fulcio 根（DER）、Rekor Ed25519 公钥
与期望的 Checkpoint origin；通过 `Policy` 提供 OIDC 签发者与 SAN 主体白名单。

## Bundle 格式

Bundle 为单个 JSON 文档（见 `BundleVersion`），字节字段使用标准 base64 编码，包含：
制品摘要、ECDSA 签名、叶证书与中间证书链、Rekor 日志条目、`integratedTime`、
RFC 6962 包含证明哈希路径、Signed Checkpoint 与可选 SET。

## 用法

```go
bundle, err := sigverify.ParseBundle(rawBundle)
if err != nil { /* ... */ }

result, err := sigverify.Verify(bundle, artifactBytes, trust, policy)
if err != nil {
    // err 可通过 errors.Is 映射到 sigverify.Err* 哨兵错误
}
// result.OIDCIssuer / result.Subjects / result.IntegratedTime
```

完整可运行示例见 `example_test.go`。

## 测试

- `TestInclusionProofAcrossAllTreeShapes`：对 1..64 个叶子的所有树型与所有索引做
  属性测试，用独立实现的 Merkle 根与包含路径交叉验证；
- `TestVerifyRejectsAttackSamples`：20 个攻击/畸形样本全部拒收，覆盖伪造 Merkle
  路径、超窗时间戳、OIDC 发行者不匹配、日志摘要/签名/证书调包、伪造 Checkpoint 与
  SET、缺失 EKU、不可信根等；
- 边界测试包含证书有效期窗口的含等号边界。
