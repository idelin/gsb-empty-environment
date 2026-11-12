# BOLT 11 发票准入

攻击者给出的BOLT 11发票可能伪造网络前缀、让签名恢复出另一个收款公钥，或用路由提示带出过高费用。给Go支付节点写供发票校验调用的准入包：网络前缀须等于调用方配置，签名恢复公钥须与n字段一致，p字段恰有一个且为52字符；过期默认3600秒；r提示的累计费用占比与cltv_expiry_delta有上限。用跨网发票、篡改n字段和高费提示验证。

## 用法

```go
inv, err := netsec.Validate(invoice, netsec.Config{
    Network: netsec.Testnet,
})
if err != nil {
    // errors.Is(err, netsec.ErrNetworkMismatch)、ErrSignature、ErrPolicy 等
}
_ = inv.Expiry // x 字段；缺省为 3600 秒
```

`Config` 的零值限额会选择安全缺省值：

- `DefaultExpirySeconds = 3600`
- `DefaultMaxFeeRatePPM = 100000`（路由提示复利累计费用 10%）
- `DefaultMaxCLTVExpiryDelta = 2016`（路由提示 CLTV 增量总和）

准入过程会校验 bech32、HRP 网络前缀、时间戳、p/n 字段、签名恢复公钥、
路由提示结构，并把 `RouteHintFeePPM` 与 `RouteHintCLTVDelta` 和调用方限额比较。
发票时间戳和有效过期秒数分别保存在 `Invoice.Timestamp` 与 `Invoice.Expiry`。

运行验证：

```sh
go test ./...
```
