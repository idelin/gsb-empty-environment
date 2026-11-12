# 预证书poison与SCT重建

证书透明度检查要从最终证书重建预证书TBS，攻击者可把预证书当最终证书用，或让SCT列表扩展参与摘要。为Go证书校验包实现SCT检查，供TLS握手后回调调用：预证书须含关键poison扩展，最终证书不得含；重建TBS时移除SCT列表，签发者公钥哈希取自实际签发者，SCT时间戳不得晚于验证时间。用带poison证书、篡改扩展和合法证书验证。

## 实现

代码位于 `sct.go`，按 RFC 6962 验证嵌入在最终证书 SCT 列表扩展
（`1.3.6.1.4.1.11129.2.4.2`）中的 Signed Certificate Timestamp。

### 防攻击要点

- **预证书不得当最终证书**：最终叶子证书若含关键 poison 扩展
  （`1.3.6.1.4.1.11129.2.4.3`）直接返回 `ErrPoisonInFinal`；预证书路径则强制
  要求 poison 扩展存在、`critical` 且值为 ASN.1 `NULL`，否则返回
  `ErrMissingPoison` / `ErrInvalidPoison`。
- **SCT 列表不参与摘要**：重建预证书 TBS 时剥离 SCT 列表扩展再追加 poison，
  其余 TBS 字段以 `asn1.RawValue` 原样保留，保证重编码字节一致；针对“带 SCT
  列表的 TBS”签发的时间戳会因签名不匹配被拒绝。
- **签发者公钥哈希取自实际签发者**：签名输入中的 `issuer_key_hash` 由传入
  签发者证书的 `RawSubjectPublicKeyInfo` 计算 SHA-256；用错签发者即验签失败。
- **时间戳不得晚于验证时间**：SCT 的毫秒时间戳晚于 `VerifyOptions.Now`
  （默认当前时间）返回 `ErrSCTInFuture`，相等视为合法。
- **只信任已登记日志**：按日志 ID（SPKI 的 SHA-256）在 `LogStore` 中查找，
  未知日志返回 `ErrUnknownLog`；支持 ECDSA 与 RSA(PKCS#1 v1.5) 日志、
  SHA-256/SHA-384。

### 用法

```go
log, _ := netsec.NewCTLog(logPublicKey)          // 信任的 CT 日志公钥
store := netsec.NewLogStore(log)

// TLS 握手后回调：普通链校验通过后再做 CT 检查
tlsConfig := &tls.Config{
    VerifyConnection: store.VerifyConnection,
}

// 或在固定时间点显式校验（测试/审计场景）
err := store.VerifyEmbeddedSCTs(leafCert, issuerCert, &netsec.VerifyOptions{
    Now: verifyTime,
})

// 直接校验预证书与独立分发的 SCT
err = store.VerifyPrecertificateSCTs(precert, issuer, rawSCTs, opts)
```

`VerifyConnectionAt(cs, t)` 可注入验证时间；回调优先使用
`cs.VerifiedChains` 中的实际签发者，其次回退到对端链。

## 测试

`sct_test.go` 自建 CA 与 CT 日志密钥，端到端模拟“预证书 -> 签发 SCT ->
嵌入最终证书”的流程，覆盖：

- 重建的预证书 TBS 与真实预证书逐字节一致、合法证书验签通过（ECDSA/RSA、
  SHA-256/SHA-384）。
- 带 poison 的证书作为最终证书被拒；poison 缺失/非关键/值错误的预证书被拒。
- 篡改扩展（SAN、TBS）与“让 SCT 列表参与摘要”均返回 `ErrSCTSignature`。
- 时间戳在未来被拒（含相等边界）、未知日志被拒、缺失/畸形 SCT 报错。
- 签发者公钥哈希绑定实际签发者，以及 TLS 握手回调各路径。

运行：

```sh
go test ./...
go test -race -cover ./...
```
