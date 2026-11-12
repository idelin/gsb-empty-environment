# SAE防阻塞令牌

WPA3-SAE接入点遇到洪泛commit会耗尽曲线计算，攻击者还可反射接入点自己的commit。为Go接入点控制面实现前置过滤包，供Authentication帧处理调用：未完成事务超阈值后只回anti-clogging token（轮换密钥对站点MAC和群编号做HMAC）；scalar在2到r-1之间，element是非无穷曲线点且不等于己方值。用洪泛、重放token、反射和合法握手验证。

## 实现

- 支持 SAE group 19/20/21（NIST P-256/P-384/P-521）。
- `Filter.HandleCommit` 分三档返回 `ActionProcess`、`ActionSendToken` 或 `ActionDrop`；达到 `MaxIncomplete` 后，无 token 的 commit 不做曲线运算，只得到新 token。
- 失败的昂贵校验（曲线点检查、反射检查）在 `FailureTTL` 窗口内计入压力，防止攻击者用无穷多个非法 element 绕过阈值、持续刷新免费验算额度。
- scalar 必须是该群固定宽度编码，并满足 `2 <= scalar <= r-1`。
- element 接受 SAE 的 raw `x||y`、SEC1 未压缩和压缩编码；拒绝无穷点、非曲线点和与 AP 己方 element 相等的点（按曲线点比较，不依赖编码）。
- token 为 `nonce || HMAC-SHA256(epoch key, nonce || STA MAC || group)`。epoch key 由主密钥按 `TokenRotation` 派生并轮换；token 绑定站点 MAC、群编号和签发 epoch，`Filter` 在 token 成功用于一次 commit 后记录其重放状态。
- 同一 station/group 的相同 commit 重传返回 `Duplicate=true`，处理器应重发已缓存的响应，不再做曲线运算；同一 station/group 的不同 commit 在旧事务完成前被丢弃。

## Authentication 帧处理

```go
filter, err := netsec.NewFilter(netsec.Config{
    MaxIncomplete: 32,
    TokenSecret:   secret,           // 至少应妥善保管；为空时随机生成
    OwnElements: map[netsec.GroupID][]byte{
        netsec.Group19: apP256Element, // AP 当前己方 public element
    },
})
if err != nil {
    return err
}

decision := filter.HandleCommit(netsec.CommitRequest{
    StationMAC: staMAC,
    Group:      netsec.Group19,
    Scalar:     scalar,
    Element:    element,
    Token:      antiCloggingToken, // 重传的 STA commit 中携带的 token
})

switch decision.Action {
case netsec.ActionProcess:
    if decision.Duplicate {
        sendCachedSAEResponse()
        return
    }
    // 交给 SAE 状态机；Confirm 成功或事务终止时调用：
    // defer filter.Complete(staMAC, netsec.Group19)
    processSAECommit()
case netsec.ActionSendToken:
    sendOnlyAntiCloggingToken(staMAC, decision.Token)
case netsec.ActionDrop:
    // 静默丢弃；decision.Reason 可用于内部审计
}
```

`APElement` 也可以在每次交换时随 `CommitRequest` 传入，用于校验动态生成的己方值。

## 验证

`filter_test.go` 覆盖：

- 洪泛：达到阈值后只发 token，曲线校验计数不再增长；
- 非法 element 洪泛：失败验算计入压力，不能无限刷新免费曲线运算；
- token 重放：同一 token 二次使用被拒绝并返回新 token；token 跨站点、跨群、跨密钥 epoch 均失败；
- 反射：raw/压缩编码的 AP 己方 element 均被拒绝；
- 合法握手：三个群的 scalar/element 通过，token 可通过压力门控，事务完成后计数回落。

```sh
go test -race ./...
```
