# I2P LeaseSet时效与旧版本替换

I2P 的 LeaseSet2 告诉别人通过哪些网关到达某个目的地，攻击者可重放旧版本把流量引向已失效或自己控制的网关。给Go netDb 写供存储请求调用的 LeaseSet 准入包，签名验证由调用方注入，以下按本题规定：published 须严格大于已存的同一目的地条目，否则丢弃；published 晚于当前时间 60 秒以上拒绝；expires 偏移不超过 660 秒；每个租约结束时间不得晚于 published 加 expires，租约数 1 至 16，且已过期的租约在返回时被剔除；使用离线签名时，离线签名自己的过期时间不得早于 LeaseSet 的 expires，且不得已过期。用旧版本重放、未来 published、超长 expires、离线签名过早到期验证。

## 准入包用法

`Admit(ls, store, verify, now)` 是存储请求入口，返回过滤掉已过期租约的副本（不修改入参），拒绝时返回可经 `errors.Is` 匹配的哨兵错误：

- `ErrReplay`：同一目的地已存在条目的 `published` 不小于入站值（重放/旧版本）。
- `ErrFuturePublished`：`published` 严格晚于 `now + 60s`。
- `ErrExpiresOutOfRange`：`expires` 偏移不在 `(0, 660s]`。
- `ErrLeaseCount`：租约数不在 `[1, 16]`（过滤前校验）。
- `ErrLeaseBeyondExpiry`：租约 `ends` 晚于 `published + expires`。
- `ErrOfflineExpired` / `ErrOfflineTooShort`：离线签名已过期，或早于 LeaseSet 过期时间。
- `ErrSignature`：注入的 `Verifier` 拒绝签名；`ErrInvalidArgument`：参数为 nil。

```go
store := netsec.NewMemoryStore()
verify := func(ls *netsec.LeaseSet) error {
    // 调用方自行完成目的/临时密钥及离线签名的密码学校验
    return nil
}
accepted, err := netsec.Admit(ls, store, verify, time.Now())
if err != nil {
    return err // errors.Is 匹配具体规则
}
store.Put(accepted)
```

时间 `now` 显式注入便于测试；生产路径可用 `AdmitNow(ls, store, verify)`。

## 测试

```sh
go test -race ./...   # 含旧版本重放、未来 published、超长 expires、离线签名过早到期等场景
```
