# WS-Security 1.1 头校验包（Go）

可导入的 Go 包 `netsec`，为 SOAP 网关执行 WS-Security 1.1 头策略，防止旧报文重放以及“挪走已签名 Timestamp、另放新 Timestamp”的篡改。

## 校验策略

- `wsse:Security` 中**恰有一个** `wsu:Timestamp`：缺失或多于一个均拒绝。
- `wsu:Created` 相对当前时间的未来偏移不得超过 `MaxCreatedSkew`（默认 60s）。
- `wsu:Expires` 必须晚于 `Created`，间隔不得超过 `MaxTimestampTTL`（默认 300s）；当前时间到达或超过 `Expires` 即按过期拒绝。
- `wsu:Id` 全局唯一：`Timestamp` 与 SOAP `Body` 各自必须恰好携带一次 `wsu:Id`，任何 Id 重复都拒绝。
- 签名引用绑定：调用方注入 `SignatureVerifier` 完成 XML-DSIG 密码学校验并返回已签名引用集合；`Timestamp` 与 `Body` 的 Id 不在集合中则拒绝（防止未签名 Timestamp 替换）。
- `UsernameToken` 的 `(Nonce, Created)` 组合在时间窗内仅允许使用一次，通过可插拔 `NonceStore` 实现；内置进程内带 TTL 的 `MemoryNonceStore`，分布式部署可自行实现 Redis `SET NX EX` 等后端。
- 命名空间按 URI 匹配，与文档使用的前缀无关；兼容 SOAP 1.1 与 1.2。

## 用法

```go
import netsec "example.org/netsec/q0355"

// verify 由网关基于自己的 XML 签名栈实现，返回已签名的 Id 引用。
verify := func(ctx context.Context, msg []byte) (*netsec.SignatureResult, error) {
    // ... XML-DSIG 规范化、摘要与签名值校验 ...
    return &netsec.SignatureResult{SignedReferences: []string{"#TS-1", "#Body-1"}}, nil
}

v := netsec.New(verify, netsec.NewMemoryNonceStore())
if err := v.Validate(ctx, request.Body); err != nil {
    // err 包装稳定哨兵错误，可用 errors.Is 映射为 SOAP fault
}
```

所有拒绝都返回包装哨兵错误的 `*ValidationError`，可用 `errors.Is(err, netsec.ErrReplay)` 等判定，完整列表见 `errors.go`。

## 测试

```sh
go test -race -count=1 ./...
```

覆盖：Nonce 重放、双 Timestamp、未签名 Timestamp/Body、`wsu:Id` 重复、时间窗与 TTL 边界、过期、畸形 XML、前缀/SOAP 版本无关性、并发下 Nonce 仅一次成功，以及被拒报文不污染 Nonce 缓存。
