# MQTT 5 会话恢复授权

本包（`netsec`）提供 MQTT 5 Broker 在 CONNECT 阶段使用的会话恢复逻辑，重点防御“猜测他人 ClientID 以继承订阅和离线消息”的会话劫持。

## 安全行为

- 会话在首次创建时绑定认证主体。`Clean Start=0` 且主体不一致时，旧订阅和队列全部丢弃，`Session Present` 返回 `0`，新连接得到空会话。
- 同一主体再次使用相同 ClientID 时执行接管：旧连接先被 `InvalidateInflight()` 使在途发布失效，再收到 `SessionTakenOver()`，应发送 MQTT 5 DISCONNECT reason `0x8E`。
- 不同主体不会收到接管通知，避免攻击者仅凭 ClientID 踢掉合法连接。
- 恢复时按当前 ACL 重新检查每个订阅和队列中的 QoS 1 消息；被撤销的订阅/消息直接丢弃。`NextDelivery()` 在每次投递前再次检查，因此 CONNECT 之后发生的撤权也会生效。
- `Manager.Recover` 对同一 ClientID 的恢复/关闭操作串行化；并发接管时只会有一个有效会话。

## CONNECT 集成

```go
m := netsec.NewManager(acl) // acl 实现 netsec.Authorizer

recovery, err := m.Recover(netsec.ConnectRequest{
    ClientID:   connect.ClientID,
    Principal:  authenticatedPrincipal,
    CleanStart: connect.CleanStart,
}, brokerConn) // brokerConn 实现 netsec.Connection
if err != nil {
    // reject CONNECT
}

// CONNACK.SessionPresent = recovery.SessionPresent()
for _, sub := range recovery.Subscriptions() {
    // install the still-authorized subscription
}
for {
    delivery, ok := recovery.NextDelivery()
    if !ok {
        break
    }
    // send delivery.Message with delivery.Subscription's QoS
}
```

连接关闭时调用 `recovery.Close()`，会话会保留供 `Clean Start=0` 恢复。需要提前过期离线会话时调用 `m.Delete(clientID)`。

`Connection.InvalidateInflight()` / `SessionTakenOver()` 在 Manager 锁外调用；旧连接的在途发布在回调前已因 `Recovery.Valid()` 变为 `false` 而失效。Broker 在完成任何在途发布前应检查 `recovery.PublishAllowed()`。

## 验证

测试覆盖主体不一致、撤权后重连、投递前动态撤权以及并发接管竞态：

```sh
go test -race ./...
```
