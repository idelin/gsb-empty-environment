# WPA3-SAE commit 前置过滤器

本包用于 Go 接入点的 Authentication 帧控制面，在任何 SAE 曲线计算之前过滤
Commit。它同时防御 commit 洪泛、anti-clogging token 重放和 AP commit 反射。

## 行为

- `Threshold` 是未完成 SAE 事务的准入上限。达到上限后，新的 Commit 不会做
  标量或曲线点校验，只返回 anti-clogging token。
- token 为 `随机 nonce || HMAC-SHA256(tag)`。HMAC 绑定站点 MAC、SAE 群编号
  和 nonce，密钥按 `KeyRotation` 轮换；旧密钥在 `TokenTTL` 内保留以接受在途
  token。
- token 成功后只能使用一次；成功准入的 token 在 TTL 内重放会得到
  `ErrTokenReplay`。不同站点 MAC 或不同群编号不能复用 token。
- 准入前校验 scalar 位于 `[2, r-1]`，element 是所选群上的非无穷点。
- `Commit.LocalElement` 是 AP 本次握手的己方 element；相同点（包括不同 SEC1
  编码）会被 `ErrReflectedCommit` 拒绝。
- 支持的 NIST 群：19、20、21、25、26。element 接受 SAE 的定长 `x||y`
  编码，也接受 SEC1 未压缩/压缩编码。

## Authentication 接入

```go
filter, err := netsec.NewFilter(netsec.Config{
    Threshold:          32,
    KeyRotation:        30 * time.Second,
    TokenTTL:           2 * time.Minute,
    TransactionTimeout: 2 * time.Minute,
})
if err != nil {
    return err
}

result := filter.HandleCommit(peerMAC, netsec.Commit{
    Group:        group,
    Scalar:       peerScalar,
    Element:      peerElement,
    LocalElement: apElement,
    Token:        receivedToken, // 首次为空
})

switch result.Action {
case netsec.ActionAccept:
    // 只有此时可以执行 SAE 曲线计算。
case netsec.ActionChallenge:
    // 只回 result.Token；不要执行曲线计算。
case netsec.ActionReject:
    // 丢弃帧；result.Err 说明原因。
}
```

`ActionAccept` 会占用一个未完成事务槽；握手完成或中止时必须调用
`filter.Complete(peerMAC)`。同一 Commit 的重传返回
`ActionAccept + Duplicate`，不会重复占用槽位。

## 验证

测试覆盖洪泛、token 重放/跨站点复用、反射 commit、scalar/element 边界、
密钥轮换以及带 token 的合法 P-256 握手：

```sh
go test -race ./...
```
