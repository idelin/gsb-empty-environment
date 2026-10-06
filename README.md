# devsched

单在途设备请求调度库。设备一次只允许一个未完结请求；响应可能晚于超时到达，
事务号（txn ID）循环复用。库为每个事务号设置隔离期，保证迟到应答永远不会
被误认为新请求的响应。

## 设计

- **单在途**：任意时刻至多一个请求在设备上，其余排队（`Submit` 阻塞等待结果）。
- **事务号隔离期**：ID 释放后进入隔离期（`WithIsolation`，默认 5s），期满前不得
  复用。隔离期必须大于设备迟到响应的最大可能延迟。所有 ID 都在隔离期时，新请求
  排队等待，库会在最早隔离期满时自动唤醒派发。
- **迟到应答作废**：`HandleResponse` 只接受与当前在途请求事务号匹配的响应，其余
  一律丢弃并计入 `Stats().StaleResponses`——迟到应答不得完成新请求。
- **队列取消不影响在途**：`Submit` 的 ctx 取消时，排队中的请求直接出队；在途请求
  的调用方立即拿到 `ctx.Err()`，但设备槽位仍占用直到响应或超时到达（设备状态
  无法回滚）。
- **停机排空**：`Close` 拒绝新请求、让排队请求以 `ErrClosed` 失败，并阻塞直到
  在途请求排空（响应到达或超时）。

## 用法

```go
s := devsched.New(transport,
    devsched.WithTimeout(time.Second),
    devsched.WithIsolation(5*time.Second),
    devsched.WithTxnIDSpace(256),
)
resp, err := s.Submit(ctx, payload) // 排队 → 派发 → 等待响应/超时
// 设备响应回调线程：
s.HandleResponse(txnID, payload)
s.Close() // 停机排空
```

## 虚拟时钟测试

`Clock` 接口把时间与定时器抽象出来；`VirtualClock` 只在 `Advance`/`AdvanceTo`
时推进，测试可确定性地重放：

- `TestTimeout` — 超时路径
- `TestLateResponseDropped` — 超时后迟到响应被丢弃，且不得完成新请求
- `TestIsolationDelaysReuse` — 全部 ID 隔离中时派发停滞，期满自动唤醒
- `TestWraparound` — ID 环回绕复用，绝不违反隔离期
- `TestQueueCancelLeavesInflight` — 队列取消不影响在途
- `TestShutdownDrainOnResponse` / `TestShutdownDrainOnTimeout` — 停机排空
  （响应排空 / 超时排空）

```sh
go test ./... -count=1 -race
```
