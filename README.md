# SCT数量与运营方多样性门槛

证书里嵌入的 SCT 要来自足够多且互相独立的日志，攻击者可让同一运营方的多个日志各出一份，或重复提交同一份 SCT 凑数。给Go证书校验包写供握手后回调使用的 SCT 策略组件，签名验证与日志表由调用方注入，以下为本题规定：有效期不超过 180 天的证书至少 2 份有效 SCT，超过 180 天的至少 3 份；有效 SCT 须来自至少两个不同运营方；同一 log_id 只计一份；日志状态为已退休且 SCT 时间戳晚于退休时间、或被拒绝、或待收录的不计；时间戳晚于验证时间 5 分钟以上的不计；SCT 版本不是 v1 的不计。用同运营方多日志、重复 SCT、退休后时间戳和 180 天边界验证。

## 组件

- `sctpolicy.go`：策略主体。`Policy` 持有调用方注入的 `LogList`（日志表）
  与 `SignatureVerifier`（SCT 签名验证），`Now` 可注入固定验证时间。
- `sctparse.go`：解析证书内嵌的 RFC 6962 SCT 列表扩展
  （OID `1.3.6.1.4.1.11129.2.4.2`），输出 `[]*SCT`。

### 判定规则

- 证书有效期 `<= 180` 天至少 2 份有效 SCT，`> 180` 天至少 3 份；
- 有效 SCT 必须覆盖至少 2 个不同运营方（按 `Log.Operator()` 去重）；
- 同一 `log_id` 只计一份，重复项以 `RejectDuplicateLogID` 记录；
- `rejected`、`pending` 日志的 SCT 不计；`retired` 日志在
  `RetiredAt()` 之后签发的 SCT 不计；
- 时间戳晚于验证时间 5 分钟以上的不计（恰好 5 分钟容忍）；
- 版本不是 v1 的不计；签名验证不通过的不计。

### 握手后回调接入

`Policy` 同时提供 `VerifyConnection(tls.ConnectionState)` 与
`VerifyPeerCertificate(rawCerts, verifiedChains)`，后者可直接赋给
`tls.Config.VerifyPeerCertificate`：

```go
policy := &netsec.Policy{
    Logs:     myLogList,   // 实现 netsec.LogList
    Verifier: myVerifier, // 实现 netsec.SignatureVerifier
}
tlsConfig := &tls.Config{
    VerifyPeerCertificate: policy.VerifyPeerCertificate,
}
```

需要逐项审计时调用 `Evaluate`，从 `Result.Accepted`/`Result.Rejected`
获取每份 SCT 的计入情况与拒绝原因；只需要通过/失败结论时调用
`VerifyEmbeddedSCTs`，失败返回 `*PolicyError`。
