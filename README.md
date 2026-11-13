# 密钥更新次数

对端连续发送 KeyUpdate 时，每次更新都会推进一代流量密钥。若不限制更新次数，攻击者可以迫使接收端长期保留大量旧密钥状态。本包提供一个不执行 I/O、可直接嵌入 TLS 栈的 Go 状态机。

## 行为

- 每次接受的更新只推进一代密钥。
- 未确认的更新最多挂起一条；确认前再次更新返回 `ErrKeyUpdatePending`。
- 收到确认后可以继续更新。
- 连续更新次数超过调用方上限时返回 `ErrTooManyKeyUpdates`，并保持失败状态直到 `Reset`。
- 普通应用数据（`ApplicationData`/`EventProgress`）会确认挂起更新并重置连续计数；确认本身不会重置计数，避免对端通过逐条确认绕过上限。
- `NewStateMachine(0)` 使用安全默认值 32，`Unlimited` 显式关闭上限。
- 所有方法并发安全。

## 用法

```go
m := netsec.NewStateMachine(netsec.DefaultMaxConsecutiveUpdates)

if err := m.Update(); err != nil {
	// reject the connection: the peer sent two updates without confirmation
}
if err := m.Acknowledge(); err != nil {
	// handle the peer's confirmation
}
```

TLS 栈也可以只上报事件：

```go
err := m.Apply(netsec.EventKeyUpdate)
```
