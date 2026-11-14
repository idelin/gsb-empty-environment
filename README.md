# 联系人同步范围组件（Go）

供 Go 联系人服务导入的同步范围组件。同步不再信任客户端提交的设备标识，而是把
`账户 + 已登记并批准的设备 + 单次令牌 + 版本游标` 绑定为一个服务端范围。联系人
按账户独立存储和排序，令牌只决定“允许读取哪个账户的增量”，不会根据客户端自报的
设备标识拼接查询。

## 快速使用

```go
import syncscope "example.org/netsec/q0508"

scope := syncscope.NewSyncScope()

// 设备登记与批准是两步操作；未批准设备拿不到令牌。
_ = scope.RegisterDevice("account-1", "device-1")
_ = scope.ApproveDevice("account-1", "device-1")

// 首次同步从游标 0 开始，令牌只返回一次。
token, err := scope.IssueToken("account-1", "device-1")
if err != nil {
    // 拒绝
}
result, err := scope.Sync(syncscope.SyncRequest{
    AccountID: "account-1", // 必须来自服务端已认证会话
    DeviceID:  "device-1",
    Token:     token,
    Cursor:    0,
})

// 下一次增量同步必须使用上次返回的 NextToken 与 NextCursor。
_, _ = scope.Sync(syncscope.SyncRequest{
    AccountID: "account-1",
    DeviceID:  "device-1",
    Token:     result.NextToken,
    Cursor:    result.NextCursor,
})
```

## 安全不变量

- 注册设备归属唯一账户；另一账户不能用同一设备标识注册、批准或取令牌。
- 设备必须“已登记 + 已批准”，并且每次拉取都会重新检查，撤销后已发令牌立即失效。
- 令牌为 32 字节随机值，服务端只保存 SHA-256 摘要；令牌绑定账户、设备与游标。
- 令牌单次使用并在成功同步后轮换；旧令牌重放返回 `ErrTokenUsed`。
- 游标必须与令牌绑定的值完全一致；旧游标、跳号或伪造游标返回 `ErrCursorStale`。
- 账户之间的版本、联系人 ID 和变更日志完全隔离；相同联系人 ID 不会互相覆盖或删除。
- 删除写入墓碑变更，只返回联系人 ID，不返回删除前的姓名、电话等数据；增量同步按版本
  顺序应用即可与删除结果一致。
- 所有公开方法并发安全；同步校验、读取和令牌消费在同一把锁内完成，避免校验后状态变化。

## 集成要求

- `SyncRequest.AccountID` 必须由服务端会话认证结果填入，不能直接采用请求体中的账户 ID。
- `DeviceID` 可以来自客户端，但只有已完成登记与人工/策略批准的设备才能通过。
- 设备登记与批准应由受信任的账户会话或运维流程调用，不能由匿名请求直接批准。
- 该实现是内存参考实现。需要持久化时，应把设备、令牌、账户版本、变更日志和游标放入同一
  数据库事务，保持上述绑定关系与游标单调性。

## 验证

```sh
go test ./...
go test -race ./...
```

测试覆盖设备伪造、未批准设备、令牌重放、跨账户令牌/联系人隔离、合法增量同步、旧游标
拒绝、删除墓碑以及删除不泄漏旧联系人数据等场景。
