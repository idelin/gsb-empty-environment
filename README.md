# pubsub

一个仅使用 Go 标准库实现的进程内、泛型 fan-out 发布/订阅组件。

## 语义

- **独立有界队列**：每个订阅者拥有独立的 buffered channel，互不共享。
- **慢消费者策略可选**：
  - `OverflowBlock`（默认）：队列满时本次投递阻塞，直到有空位或 `ctx` 超时/取消。
  - `OverflowDropNewest`：队列满时对该订阅者**显式丢弃**最新消息，计数累加并通过
    `OnDrop` 回调通知——绝不无声丢失。
- **慢订阅者隔离**：发布对各订阅者并发 fan-out，一个慢订阅者只会阻塞它自己的投递，
  不会影响其他订阅者收消息。
- **退订并发安全**：`Unsubscribe` 与 `Publish` 可并发；只等待该订阅者自身的在途投递。
  重复调用幂等，不 panic。
- **关闭与排空**：
  - 消息队列 channel **从不被关闭**，从结构上杜绝「向已关闭 channel 发送」。
  - `Close()` 三阶段：① 解除所有阻塞在满队列上的投递；② 等待全部在途发送结束；
    ③ 关闭 `Done()`。此时已承诺（已入队）的消息完整保留，可用 `Drain()` 排空。
  - 关闭后 `Publish`/`Subscribe` 返回 `ErrClosed`；`Close`、`Unsubscribe` 幂等。
- **任务函数 / 时间 / 失败注入**：`Serve[T]` 在订阅者侧消费队列并执行注入的
  `TaskFunc`；失败按指数退避重试（`MaxAttempts`/`InitialBackoff`/`MaxBackoff`），
  时钟通过 `Clock` 接口注入（测试用 `FakeClock`，零真实睡眠），每次失败触发
  `OnFailure` 事件。

## API 速览

```go
b := pubsub.NewBroker()
s, _ := b.Subscribe(pubsub.SubscriberConfig{
    Buffer:   8,
    Overflow: pubsub.OverflowBlock, // 或 pubsub.OverflowDropNewest
    OnDrop:   func(msg any) { /* 显式丢弃通知 */ },
})

go b.Publish(ctx, "hello")

// 消费端：Serve 或直接 range/读取 s.C()
stats := pubsub.Serve[string](ctx, s, pubsub.WorkerConfig[string]{Handler: h})

s.Unsubscribe() // 幂等、并发安全
b.Close()       // 幂等；已入队消息在 s.Drain() 中可取回
```

## 文件

- `broker.go`：`Broker` / `Subscription`，队列、策略、并发与关闭协议。
- `worker.go`：泛型 `Serve[T]` 任务执行器（重试、退避、失败事件、统计）。
- `clock.go`：`Clock`/`Timer` 抽象、墙钟实现、测试用 `FakeClock`。
- `*_test.go`：`go test` 测试（语义 + 并发 + 注入）。
- `examples/basic/main.go`：可运行示例。

## 运行测试（含竞态检测）

```sh
go test -race -count=1 ./...
```

## 运行示例

```sh
cd examples/basic && go run .
```

### 示例输出

```
published=10 delivered=13 dropped=8
slow subscriber retained=2, explicit drops=8
after close, fast drained committed=1 (incl. final=true)
```

说明：10 次发布扇出给当时存活的订阅者；慢订阅者队列容量为 2，其余 8 条被
**显式丢弃并计数**；快订阅者始终收全。关闭后，最后一条已承诺消息 `final`
仍可从队列中排空读出。
