# Cosign证书时间落点

攻击者持过期Fulcio证书并想伪造签名时间。为Go验证服务实现可导入的Cosign bundle时间校验包：SET由日志公钥验签，覆盖body、integratedTime、logIndex、logID的规范JSON；integratedTime须落在证书有效期内，且不晚于当前时间加允许偏差。用过期证书、篡改时间和换logID的bundle测试。

## `timeverify` 包

可导入路径：`example.org/netsec/q0077/timeverify`（仅依赖 Go 标准库）。

### 校验流程

`Verifier.Verify` 按以下顺序校验一个 Cosign bundle：

1. **logID 可信**：`Payload.LogID` 必须命中内置可信日志，且等于日志公钥
   DER PKIX 编码的 `hex(sha256(...))`，否则返回 `ErrInvalidLogID`。
2. **SET 验签**：用该日志公钥验证 SET。签名覆盖规范 JSON
   `{"body":...,"integratedTime":...,"logID":...,"logIndex":...}`
   （紧凑、字段顺序固定，body 为 base64 字符串）。ECDSA 走
   ECDSA-SHA256/ASN.1，Ed25519 直接对规范报文签名；任一覆盖字段被篡改都
   返回 `ErrSETVerification`。
3. **证书有效期**：签名内的 `integratedTime`（Unix 秒）必须满足
   `NotBefore <= integratedTime <= NotAfter`，分别返回
   `ErrIntegratedTimeBeforeCert` / `ErrIntegratedTimeAfterCert`。
4. **未来时间偏差**：`integratedTime` 不得晚于 `now + skew`
   （默认 `DefaultSkew = 30s`，可用 `WithSkew` 调整、`WithClock` 注入时钟），
   否则返回 `ErrIntegratedTimeInFuture`。

时间只取自被 SET 签名覆盖的 `integratedTime`，bundle 中任何未签名的时间
字段都不被信任，因此过期证书无法通过把签名时间改到有效期内来蒙混过关。

### 攻击场景与测试

| 攻击 / 场景 | 测试 | 结果 |
| --- | --- | --- |
| 过期 Fulcio 证书，伪造当前签名时间 | `TestExpiredCertForgedTime` | `ErrIntegratedTimeAfterCert` |
| 历史合法收录、证书现已过期 | `TestVerifyValidBundle` | 通过 |
| 篡改 `integratedTime`（及 logIndex/body） | `TestTamperedIntegratedTime` | `ErrSETVerification` |
| 替换成另一个日志的 logID | `TestSwappedLogID` | 未知日志 `ErrInvalidLogID`；双日志可信时 `ErrSETVerification` |
| 超出允许时钟偏差的未来时间 | `TestFutureSkew` | `ErrIntegratedTimeInFuture` |
| 早于证书 NotBefore | `TestNotBeforeRejected` | `ErrIntegratedTimeBeforeCert` |
| Ed25519 日志 / 空输入 / 规范 JSON | `TestEd25519Log`、`TestNilInputs`、`TestCanonicalJSON` | 见测试 |

### 用法

```go
trusted, _ := timeverify.NewLogKey(rekorPublicKey) // crypto.PublicKey
verifier, _ := timeverify.NewVerifier([]*timeverify.LogKey{trusted})

err := verifier.Verify(&timeverify.Bundle{
    Leaf:    fulcioCert,
    Payload: timeverify.Payload{Body: bodyB64, IntegratedTime: ts, LogIndex: idx, LogID: logID},
    SET:     signedEntryTimestamp,
})
```

运行测试：`go test ./...`，可运行示例见 `timeverify.ExampleVerifier`。
