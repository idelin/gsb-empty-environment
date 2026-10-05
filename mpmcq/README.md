# mpmcq — 区分正常/故障关闭的有界 MPMC 队列

## 关闭语义

- **正常关闭 `Close()`**：不再接受新元素（`Send` 返回 `ErrClosed`），
  缓冲中的已有元素继续留给消费者排空，排空后 `Recv` 返回 `ErrClosed`。
- **故障关闭 `Abort()`**：队列立即停止，缓冲中的剩余元素作为**余项**
  返回给 `Abort` 调用者；阻塞中的收发双方被唤醒并收到 `ErrAborted`。
- **竞争**：两种关闭并发时按**首次生效者**决定语义。`Close` 返回是否生效，
  `Abort` 生效时返回余项切片（可能为空但非 nil），未生效时返回 `nil`。

## 取消不吞元素

`Send`/`Recv` 接受 `context.Context`。元素只有在真正入队的瞬间才移交所有权；
等待期间被取消时元素仍归调用者，随 `ctx.Err()` 返回，绝不丢失。
接收取消同理，不会取走任何元素。

## 可重放历史与守恒检查

- `History()` 返回全局有序的操作事件流（入队/出队/关闭/中止/余项/取消）。
- `Replay(hist, capacity)` 重放历史并校验：序号连续、容量不越界、
  不出队不存在的元素、关闭竞争首次生效、故障关闭后余项全部返回。
- `Conservation()` 给出运行时快照，守恒恒等式：
  `Accepted == Consumed + Returned + Buffered`（所有权守恒），
  `0 <= Buffered <= Capacity`（容量守恒）。

## 测试

```sh
go test -race ./...
```
