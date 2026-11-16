# WS-Security Timestamp校验

SOAP 服务靠 WS-Security 的 Timestamp 与 UsernameToken 限制重放，攻击者拿旧报文重放，或把已签名的 Timestamp 挪走、另放一个新的。为Go的 SOAP 网关写可导入的 WS-Security 头校验包（WS-Security 1.1）：Security 头里恰有一个 Timestamp，Created 不得晚于当前时间超过 60 秒，Expires 须晚于 Created 且二者间隔不超过 300 秒，已过期拒绝；Timestamp 与 Body 的 wsu:Id 须各自恰出现一次，并被调用方注入的 XML 签名校验函数列入已签名引用集合，未被签名或 Id 重复拒绝；UsernameToken 的 Nonce 与 Created 组合在窗口内只能使用一次。测重放、双 Timestamp、未签名 Timestamp、Id 重复。

## 包：`wssecurity`

可导入子包位于 `wssecurity/`（import 路径 `example.org/netsec/q0355/wssecurity`），仅依赖标准库。

```go
validator, err := wssecurity.New(wssecurity.Config{
    // 必选：调用方实现 XML-DSig 校验并返回已签名引用（"#id" 或 "id" 均可）
    VerifySignature: func(ctx context.Context, r io.Reader) ([]string, bool, error) {
        return verifyEnvelopeSignature(r) // 接入网关自身的签名库
    },
    // 可选：默认内存 NonceCache；多实例部署可实现 wssecurity.NonceCache 接 Redis
    // MaxFutureSkew 默认 60s，MaxTimestampTTL 默认 300s
})
if err != nil { /* ... */ }

if err := validator.Validate(ctx, requestBody); err != nil {
    // 所有错误均为 *wssecurity.ValidationError，可用 errors.Is 匹配哨兵错误
    switch {
    case errors.Is(err, wssecurity.ErrReplayedNonce):
    case errors.Is(err, wssecurity.ErrUnsignedTimestamp):
    case errors.Is(err, wssecurity.ErrDuplicateID):
    }
}
```

校验规则（WS-Security 1.1，命名空间按 URI 匹配、与前缀无关，兼容 SOAP 1.1/1.2）：

- `wsse:Security` 中恰有一个 `wsu:Timestamp`，且必须位于 Security 头内。
- `wsu:Created` 相对当前时间前偏不超过 60 秒；`wsu:Expires` 晚于 Created、间隔不超过 300 秒；已过期（now >= Expires）拒绝。
- `wsu:Timestamp` 与 SOAP `Body` 各自恰有一个 `wsu:Id`，全文档 `wsu:Id` 重复即拒绝；两者都必须出现在注入签名函数返回的已签名引用集合中。
- 每个 `wsse:UsernameToken` 的 `(Nonce, Created)` 在窗口内只能消费一次，重放返回 `ErrReplayedNonce`。

测试：`go test ./...`（覆盖重放、双 Timestamp、未签名 Timestamp、Id 重复及时间窗口/前缀/缓存过期等）。
