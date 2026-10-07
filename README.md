# mailbox

一个 Go 持久化邮箱（FIFO 消息队列）库：取出不删除、确认才删、拒收重回队尾，
领取令牌绑定代际，超时重投后旧确认自动失效。

## 语义

- **取出不删除**：`Receive` 只是"领取"消息，消息在可见性超时内对其他接收者不可见，
  但仍留在队列中；只有 `Ack` 才真正删除。
- **确认才删**：`Ack(token)` 删除消息。重复确认是幂等的（返回 `nil`）。
- **拒收重回队尾**：`Nack(token)` 解除领取并把消息重新排到队列末尾，立即可见。
- **令牌绑定代际**：每次领取/重投都会把消息的代际（generation）加一，令牌只在
  当前代际有效。可见性超时后消息被重投，旧令牌再调用 `Ack`/`Nack` 返回
  `ErrStaleToken`。
- **事务发送**：`SendBatch` 把多条消息作为**一条**持久化记录写入，要么全部可见
  （按参数顺序），要么持久化失败时一条也不可见。
- **持久化优先**：所有变更先写 `Store`，成功后才应用到内存状态；持久化失败时
  队列状态保持原样。`FileStore` 是 fsync 的 JSON-lines WAL，打开时会截断
  崩溃留下的撕裂尾部；重启后通过重放恢复精确状态（包括未过期的领取）。

## 用法

```go
store, _ := mailbox.OpenFileStore("queue.wal")
defer store.Close()

q, _ := mailbox.New(store, mailbox.Options{VisibilityTimeout: 30 * time.Second})

// 事务批量发送：全有或全无
ids, _ := q.SendBatch([]byte("a"), []byte("b"), []byte("c"))
_ = ids

msg, tok, err := q.Receive()     // 领取，不删除
if err == mailbox.ErrEmpty { /* 暂无可�消息 */ }

q.Ack(tok)   // 确认删除（重复调用安全）
q.Nack(tok)  // 或拒收：重回队尾
```

## 测试

```
go test ./...
```

包含一个随机化模型测试（`TestQueueModel`），用简单的列表参考模型核对
可见顺序、重复确认与陈旧令牌行为，并在运行中反复重开队列验证恢复一致性。
