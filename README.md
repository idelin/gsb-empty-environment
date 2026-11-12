# Stellar签名载荷域分离

攻击者把Stellar公共网络的签名拿到测试网重放，或套到费用提升信封上。给Go服务写供签名核验调用的载荷与阈值包：摘要=SHA-256(网络口令哈希‖信封类型‖交易XDR)；签名提示（公钥后4字节）只用于预选，不替代验签；同一公钥重复签名只计一次权重，达到调用方阈值才通过。用跨网重放、费用提升信封和重复签名验证。

## 设计

纯标准库实现（环境无外网、无模块缓存），自带最小 XDR 编解码与 Stellar
交易信封解析，不依赖 `github.com/stellar/go`。

- `PayloadDigest`：`SHA-256(SHA-256(networkPassphrase) || envelopeType(4字节) || transactionXDR)`。
  - 网络口令哈希把载荷绑定到单一网络，公共网签名无法在测试网通过。
  - 4 字节信封类型把 v0 / v1 / fee bump 各层独立绑定，签名不能搬到费用提升外壳。
- 签名提示（公钥末 4 字节）只用于在账户签名者集合里**预选**候选；每个候选仍做
  完整 Ed25519 验签，提示碰撞或伪造提示均无法放行。
- 同一公钥的重复签名只计一次权重；权重达到调用方指定阈值才通过（`>=`，临界值即通过）。
- fee bump 信封**内外两层分别核验**，任一层失败整体失败，防止只给外层签名的包装攻击。
- 解析采用失败关闭：未知 XDR 判别值、尾部多余字节、超长数组/签名一律拒绝。

## 用法

```go
env, err := netsec.ParseEnvelopeBase64(envelopeXDRBase64)
if err != nil { /* 信封不受支持或损坏 */ }

verifier := &netsec.Verifier{
    NetworkPassphrase: netsec.PassphraseTestnet,
    Accounts: func(id []byte) (*netsec.Account, bool) {
        // 从账户库返回该 source account 的签名者与阈值
    },
    Threshold: func(a *netsec.Account) uint32 { return a.Medium },
}

result, err := verifier.VerifyEnvelope(env)
if err != nil {
    // ErrUnknownSigner / ErrInvalidSignature / ErrThresholdNotMet
}
// result.Passed；fee bump 时 result.Inner 给出内层核验明细
```

`netsec.SelectByHint` 作为独立的预选辅助函数导出，便于调用方在核验前缩小
候选范围，但它不做任何密码学判断。

## 文件

- `xdr.go` / `shapes.go`：XDR 原语与 Stellar 交易体形状（覆盖全部经典操作类型）。
- `envelope.go`：v0/v1/fee bump 信封解析与 `PayloadDigest`。
- `verify.go`：提示预选、Ed25519 验签、去重与阈值。
- `strkey.go`：Stellar G 地址（StrKey）编解码，含 CRC16-XModem 校验。
- `*_test.go`：跨网重放、fee bump 域分离与双层核验、重复签名、提示碰撞、
  篡改、多签阈值、v0 域分离、StrKey 已知向量等用例。

## 测试

```
go test ./...
go test ./... -race
```
