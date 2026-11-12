# LDAP复用连接身份降级防护

身份网关复用LDAP连接时，新绑定完成前的查询和迟到结果可能串用另一租户身份。写供Go目录客户端调用的连接租约组件，绑定操作独占连接且必须排空先前查询，绑定成功还需由注入身份确认回调核对实际主体；非空用户名配空口令即使服务端报告成功也不能授权。查询与结果同时绑定连接代次和租户租约，取消后未收齐终止响应不得复用连接。用脚本化目录服务测试绑定失败、匿名降级、取消与迟到搜索结果，确认零跨身份交付。

## 组件结构

根包 `netsec`（`example.org/netsec/q0422`）提供连接租约层，`scripted`
子包提供可脚本化的内存目录服务用于测试。

- `lease.go`：`Conn`、`Envelope`、`IdentityConfirmer`、`Lease`、`Cursor`
  等核心接口与哨兵错误。
- `manager.go` / `acquire.go`：`Manager` 与独占绑定、在途排空、连接代次
  与租户租约的发放。
- `reader.go`：唯一的读协程，对每条入站消息做代次/租约校验，迟到或跨
  身份消息在交付前即被隔离。
- `search.go`：搜索入口与游标，处理取消语义与终止响应跟踪。
- `lifecycle.go`：统计快照与关闭（等待读协程收敛，杜绝旧 Recv 消费者）。

## 安全不变量

1. **绑定独占且先排空**：`Acquire` 进入排他窗口，取消所有在途查询并等待
   其终止响应；期间搜索在 `waitWhileBinding` 阻塞。排空超时则毒化并替换
   连接，绝不复用未收齐终止响应的连接。
2. **双标签隔离**：每个查询及其结果同时绑定连接代次（generation）与租户
   租约（lease id）。读协程仅在两标签与当前绑定完全一致时才交付，其余计入
   `LateOrDropped`，跨身份消息永不到达游标。
3. **身份确认回调**：绑定协议成功后调用注入的 `IdentityConfirmer`（如
   LDAP WhoAmI）核对实际主体；主体不符、为空或回调报错均判定失败并毒化
   连接。
4. **匿名降级拒绝**：非空用户名配空口令在发起任何网络请求前即返回
   `ErrEmptyPassword`，即使服务端报告成功也不授权，并在成功路径再做一次
   纵深防御检查。
5. **取消安全**：取消后迟到条目录入 `CanceledEntriesDrained` 但不交付；
   只有终止响应被读协程收齐才允许复用同一连接。

## 用法

```go
m := netsec.NewManager(netsec.Config{
    Dialer:       myLDAPDialer,       // func(ctx) (netsec.Conn, error)
    Confirmer:    myWhoAmIConfirmer,  // netsec.IdentityConfirmer
    DrainTimeout: 5 * time.Second,
})
defer m.Close()

lease, err := m.Acquire(ctx, netsec.BindRequest{
    Tenant:   netsec.Tenant{ID: "tenant-a", BaseDN: "ou=a"},
    Username: "alice",
    Password: secret,
})
if err != nil { /* 绑定失败/匿名拒绝/身份不符 */ }

cur, err := m.Search(ctx, lease, netsec.SearchRequest{Tenant: lease.Tenant()})
if err != nil { /* 租约失效等 */ }
defer cur.Close()
for {
    entry, ok := cur.Next(ctx)
    if !ok { break }
    _ = entry
}
if err := cur.Err(); err != nil { /* 终止错误 */ }
```

## 测试

`go test -race ./...` 使用脚本化目录服务覆盖：绑定失败毒化连接、匿名降级
拒绝（含服务端报告成功）、身份确认不符/报错、取消后终止响应到达才复用、
终止缺失则毒化替换、取消后迟到条目只排空不交付、重绑定后跨身份消息零交付，
以及绑定排他窗口内搜索阻塞。
