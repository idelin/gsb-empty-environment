# HTTP二代快速重置防护

反向代理收到流建立后立即重置的请求时，昂贵后端工作仍会不断堆积。给Go代理实现可嵌入的流准入组件，把排队、授权、后端启动、重置和资源释放建成明确状态；限额同时约束活动流与尚未回收的后端工作，重置不能提前归还仍被使用的额度。用可暂停后端枚举启动与取消交错，验证旧流号回调不能影响新连接，且正常低频请求不会被一条攻击连接饿死。

## 组件

`netsec.Controller` 是可直接嵌入代理的流准入控制器，`netsec.Enumerator`
是可暂停/恢复的后端枚举器，两者都是纯库类型、无第三方依赖。

### 显式状态机

每条流的生命周期由 `StreamState` 明确表达：

- `StateQueued`：在全局 FIFO 队列中等待准入令牌；
- `StateActive`：已拿到令牌，流存活，后端尚未启动；
- `StateStarting`：后端枚举/拨号进行中；
- `StateWorking`：昂贵后端工作运行中；
- `StateReset`：客户端已重置，后端工作继续排空；
- `StateReleased`：终态，额度已归还，票据失效。

非法跳转返回 `ErrInvalidTransition`；已完全释放或归属代次不匹配的票据，
任何回调都返回 `ErrStaleTicket` 且不产生副作用。

### 两阶段额度释放

准入成功即持有一个 in-flight 令牌。令牌同时代表“活动流”和“尚未回收的
后端工作”，只有当流侧（`MarkReset`/`StreamEnd`）与工作侧
（`WorkFinished`）都结束时才归还。因此快速重置只能关闭流侧，仍在运行或
排队的后端工作会继续占用额度，`MaxInFlight` 不会被 RST_STREAM 绕过。
后端工作从未启动时可用 `Abandon` 一次性释放两侧。

### 票据代次与旧回调隔离

`Ticket` 内部携带流代次与连接代次：

- 同一 `(ConnID, StreamID)` 复用时得到新的流代次，旧票据无法命中新条目；
- 连接关闭后，仍在排空的条目标记为 `detached`，与连接命名空间解耦；
  重新 `OpenConn` 同一 `ConnID` 是全新连接代次，迟到的旧流号回调一律
  返回 `ErrStaleTicket`，不可能触碰新连接。

### 调度与防饿死

所有连接共享一条 FIFO 队列，调度时跳过受 `MaxActivePerConn` 限制的队头，
继续授予其后符合条件的流。单条攻击连接占满自身配额后，只会阻塞自己的
队头，其他连接的正常低频请求持续获得令牌，等待长度有界。

### 可暂停后端枚举

`Enumerator` 按顺序逐个返回后端，`Pause` 让后续 `Next` 阻塞在闸门上，
`Resume` 统一放行，上下文取消时不消耗候选后端。配合
`Controller.RunBackend` 可在测试中精确交错“启动 / 重置 / 取消”：
`ResetContext` 把流重置桥接为 `context.Context` 取消，后端工作及时停下，
工作侧额度仍保证恰好回收一次。

## 典型用法

```go
ctl := netsec.New(netsec.Config{
    MaxInFlight:      256,
    MaxActivePerConn: 16,
    MaxQueuedPerConn: 64,
})
ctl.OpenConn(connID)

ticket, err := ctl.Admit(req.Context(), connID, streamID)
if err != nil {
    return err // ErrQueueFull / ErrConnectionClosed / ErrControllerClosed
}

// 传输层收到 RST_STREAM 时：
// _ = ctl.MarkReset(ticket)
// 正常结束时：
// _ = ctl.StreamEnd(ticket)

err = ctl.RunBackend(ctx, ticket, enumerator, func(ctx context.Context, b netsec.Backend) error {
    return expensiveCall(ctx, b.Addr)
})
```

## 验证

`go test -race ./...` 覆盖以下关键性质：

- 重置后令牌仍被持有，后端工作回收后才放行排队流；
- 80 路快速重置下 in-flight 峰值不超过限额，最终归零；
- 关闭连接后复用同一连接号与流号，旧票据回调全部判为陈旧；
- 攻击连接占满配额时，另一连接的 12 个低频请求全部及时完成；
- 枚举器暂停/恢复/取消交错时，取消不消耗候选、启动顺序可控；
- 队列满、排队取消、`Shutdown` 与 `WaitDrained` 的行为均有断言。
