# SSH扩展请求答复关联

同一SSH连接存在多个全局请求时，成功答复没有独立编号，错误关联可能放开未授权转发。实现供Go会话宿主调用的请求队列，按发送完成次序匹配要求答复的请求，无答复请求不占位；部分写入失败关闭该连接而非重排。用取消、迟到答复及半关闭测试不会把一个批准套给另一请求。

## 用法

`Queue` 串行化一条 SSH 连接上的全局请求写入，并把
`SSH_MSG_REQUEST_SUCCESS/FAILURE` 严格按发送完成次序（FIFO）关联到待答复请求：

- `Send`：`WantReply=false` 的请求写完即返回，不占用答复槽；`WantReply=true`
  阻塞至对应答复，返回的 `approved` 表示是否成功。
- `DeliverReply`：宿主读循环解析出答复后调用，总是匹配最老的未答复槽。
- 写入部分成功（短写或报错）即视为致命错误：关闭连接、令所有在途请求失败、
  不重试也不重排；随后所有 `Send`/`DeliverReply` 返回 `ErrQueueClosed`。
- 上下文取消不会让出槽位：迟到答复仍按序被吸收丢弃，杜绝把 A 请求的批准
  套给 B 请求；无匹配请求的答复以 `ErrUnsolicitedReply` 失败并关闭队列。
- 读循环遇到 EOF（含对端半关闭）或解码错误时调用 `Close(cause)`，在途请求
  以包裹了 `ErrQueueClosed` 的错误失败，而不会继承他人的批准。

```go
queue := netsec.NewQueue(conn)

// 读循环
go func() {
    msg, err := readPacket(conn)
    if err != nil {
        queue.Close(err) // EOF / 半关闭 / 解码错误
        return
    }
    if approved, ok := netsec.ParseReply(msg); ok {
        _ = queue.DeliverReply(approved)
    }
}()

// 业务请求（例如 tcpip-forward）
approved, err := queue.Send(ctx, netsec.Request{
    Type: "tcpip-forward", WantReply: true, Payload: payload,
})
```
