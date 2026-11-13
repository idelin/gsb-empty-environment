# 缺少早期数据结束

客户端不发送EndOfEarlyData就直接发握手完成，早期数据和应用数据的边界消失。接受了早期数据时，必须先收到EndOfEarlyData再接受后续握手报文；缺失则失败。完整顺序的早期数据会话成功。可导入的Go状态机接TLS栈送来的状态。删掉EndOfEarlyData这一条报文。

## 用法

```go
m := netsec.NewStateMachine(netsec.WithEarlyDataAccepted())

// 客户端跳过 EndOfEarlyData：失败。
if err := m.Feed(netsec.Finished); !errors.Is(err, netsec.ErrMissingEndOfEarlyData) {
	// handle error
}

// 完整顺序：成功。
n := netsec.NewStateMachine(netsec.WithEarlyDataAccepted())
err := n.FeedAll(netsec.EarlyData, netsec.EndOfEarlyData, netsec.Finished)
```

状态机也接受 `State`、`MessageType`、数字握手类型和字符串事件。需要省略
EndOfEarlyData 的传输（例如 QUIC）可使用：

```go
m := netsec.NewStateMachine(netsec.WithEarlyDataAccepted(), netsec.WithoutEndOfEarlyData())
```
