# SCT来源固定

证书里的SCT签名日志不在钉住的日志集合中。至少一张SCT的日志标识在钉住集合内且签名覆盖该证书；否则按策略失败。命中钉住日志的SCT放行。可导入的Go库供TLS栈调用。换成未钉住日志的SCT。

## 策略

`netsec` 是供 TLS 栈导入的 Certificate Transparency 日志钉住库：

- 解析证书 `1.3.6.1.4.1.11129.2.4.2` 扩展中的 SignedCertificateTimestampList（RFC 6962）。
- 日志标识 `LogID` 定义为日志公钥 SubjectPublicKeyInfo DER 的 SHA-256。
- 只有当某张 SCT 的 `LogID` 属于钉住集合，且其签名按 RFC 6962 §3.2 验证通过并覆盖该证书时才放行。
- 来自未钉住日志的 SCT 一律不能单独放行；若没有任何一张钉住 SCT 验证通过，返回可 `errors.Is` 匹配的 `ErrPolicyFailed`；无 SCT 返回 `ErrNoSCTs`。
- 支持 ECDSA（P-256/P-384/P-521，SHA-256/384/512）、RSA PKCS#1 v1.5 与 Ed25519 日志密钥。

## 用法

```go
import "example.org/netsec/q0237"

pinned, err := netsec.NewPinnedLog(ctLogPublicKey) // LogID 自动由公钥派生
policy, err := netsec.NewPolicy(pinned)

// 已解析的证书：自动从 SCT 列表扩展提取 SCT
if err := policy.VerifyCertificate(leaf); err != nil {
    // 未命中任何可验证的钉住日志 SCT，按策略失败处理
}

// 或者由 TLS 栈自行提供证书 DER 与每条序列化 SCT
if err := policy.VerifySCTs(leafDER, rawSCTs); err != nil {
    // err 可通过 errors.Is(err, netsec.ErrPolicyFailed) / ErrNoSCTs 判定
}
```

`netsec.EmbeddedSCTs` 可单独从 `*x509.Certificate` 提取序列化 SCT；
`netsec.ParseSCT` / `netsec.ParseSCTList` / `netsec.MarshalSCTList` 提供线格式编解码。

## 测试

```sh
go test ./...
```
