# MQTT会话继承授权

## 威胁模型

MQTT 5 的 CONNECT 在 Clean Start = 0 时按 Client ID 恢复持久会话。若只按
Client ID 关联会话，攻击者可使用受害者的 Client ID 配合自己的凭据连接，从而
继承受害者的订阅与离线排队消息。

`session` 子包是供 CONNECT 处理器调用的会话恢复组件，强制三条安全性质：

1. **主体绑定**：会话在创建时绑定首次认证主体（`Principal`）。后续 CONNECT
   的主体不一致时，旧会话的订阅、队列、在途消息全部丢弃并重新绑定为新主体，
   CONNACK 的 Session Present 固定为 0，攻击者拿不到任何旧状态。
2. **同主体接管**：同主体重连时旧连接收到且仅收到一次
   Session Taken Over 通知（DISCONNECT reason `0x8E`）；旧连接上未确认的
   PUBLISH 在途状态立即失效——迟到的 PUBACK/PUBREC 返回 `ErrStaleHandle`，
   消息本身重排队给新连接重投。
3. **恢复即重授权**：恢复时对每个保留订阅调用 `ACL.CanSubscribe`、对每条排队
   QoS 1 消息调用 `ACL.CanReceive`，按当前策略重检；被撤销的条目不投递，直接
   丢弃并记入 `RestoreResult.DroppedMessages` 供审计。

## CONNECT 处理器集成

```go
store := session.NewStore()

res, err := store.Restore(ctx, session.RestoreRequest{
    ClientID:   packet.ClientID,
    Principal:  session.Principal{Name: authenticatedUser},
    CleanStart: packet.CleanStart,
    ACL:        currentACL,   // 每次连接的当前策略，禁止传 nil
    Conn:       networkConn,  // 实现 session.Connection
})
if err != nil { /* ErrEmptyClientID / ErrNilACL */ }

sendCONNACK(res.SessionPresent) // 直接映射 CONNACK Session Present 位
auditDropped(res.DroppedMessages)

// 优雅断开：持久会话保留用 Detach；Clean Start 会话用 Terminate 删除
defer res.Handle.Detach()
```

接管通知在锁外通过 `Connection.SessionTakenOver` 回调投递；读循环也可以
select `ConnHandle.TakenOver()` 通道及时停包。所有发送动作先
`MarkSending` 登记 packet ID，确认时 `AckPublish`，离线消息用
`NextPending` 取出。

## 验证

`session/session_test.go` 覆盖要求的三类场景（均通过 `go test -race`）：

- `TestRestore_PrincipalMismatch_DiscardsSession`：攻击者用他人 Client ID
  连接，Session Present = 0，订阅/队列/在途全部清空；受害者之后重连也是全新
  会话，旧句柄的任何写操作返回 `ErrStaleHandle`。
- `TestRestore_RevokedPermissions_FilteredOnReconnect`：撤权后重连，被撤销
  的订阅与排队消息在投递前被过滤，在途消息重投前同样过当前 ACL。
- `TestRestore_TakeoverRace`：24 个并发 CONNECT 抢同一 Client ID，最终恰好
  一个句柄持有会话，其余 23 个连接各收到一次接管通知，迟到 PUBACK 全部失效。

另有 Clean Start 清空、`Terminate` 防陈旧句柄删会话、MQTT 通配符匹配
（`TopicMatch`，含 `$SYS` 隔离）等边界测试。
