# 有界队列发布订阅（Go 标准库）

进程内泛型 Pub/Sub：每个订阅者拥有独立的有界队列，慢消费者策略可选
**阻塞背压**或**显式丢弃**；取消订阅与发布并发安全；关闭保证已承诺消息的
排空语义。仅使用 Go 标准库与标准同步原语（`sync.Mutex/RWMutex/WaitGroup/Once`、
`context`、channel）。

## 设计要点

- **独立有界队列**：每个订阅者持有一个容量为 N 的缓冲 channel，该 channel
  本身即有界队列，背压由 channel 原生提供。
- **慢消费者隔离**：`Publish` 为每个订阅者在独立 goroutine 中并发入队并等待
  全部完成。`PolicyBlock` 的慢订阅者只会延迟本次 `Publish` 返回，其他订阅者
  的入队在其等待期间已经完成，不会无声丢消息。
- **显式丢弃**：`PolicyDropNewest` 使用非阻塞 `select`，队列满时丢弃新消息，
  丢弃数经 `Subscription.Dropped()` 统计，并可通过 `WithOnDrop` 回调感知。
- **并发安全的取消订阅**：`Unsubscribe` 由 `sync.Once` 保护，重复调用幂等不
  panic；取消时取消一个 per-subscription 的 `closeCtx`，唤醒阻塞的发布者。
- **杜绝向已关闭 channel 发送**：channel 只有一个关闭点——独立收尾 goroutine
  在 `offerWG.Wait()`（所有在途入队结束）之后执行 `close(ch)`。关闭后新发布
  不再入队，因此发送与关闭永不并发。
- **关闭排空语义**：`Broker.Close` 先等待所有在途 `Publish` 完成入队（已承诺
  消息），再对每个订阅者停止接收并等待收尾；已进入订阅者队列的消息保留在
  `C()` 中，消费者继续 `range` 即可排空。`Close` 幂等；关闭后 `Publish`/
  `Subscribe` 返回 `ErrClosed`。
- **任务/时间/失败注入**：`WithHandler` 注入处理任务，失败按 `WithMaxAttempts`
  重试、按 `WithBackoff` 退避、用尽后调用 `WithOnFailed`；时钟通过 `WithClock`
  注入（测试使用确定性假时钟，见 `pubsub/fake_clock_test.go`）。

## API 速览

```go
b := pubsub.New[T](pubsub.WithClock(clock))

sub, _ := b.Subscribe(
    pubsub.WithCapacity[T](64),
    pubsub.WithPolicy[T](pubsub.PolicyBlock),       // 或 PolicyDropNewest
    pubsub.WithOnDrop[T](func(m T) { /* 丢弃感知 */ }),
)

res, err := b.Publish(ctx, msg) // res.Delivered / res.Dropped
for m := range sub.C() { ... }
sub.Unsubscribe()               // 幂等
_ = b.Close(ctx)                // 等待在途发布排空
```

注入处理任务：

```go
sub, _ := b.Subscribe(pubsub.WithHandler(
    func(ctx context.Context, m T) error { return handle(m) },
    pubsub.WithMaxAttempts[T](3),
    pubsub.WithBackoff[T](func(n int) time.Duration { return time.Duration(n) * 10 * time.Millisecond }),
    pubsub.WithOnFailed[T](func(m T, err error) { /* 死信 */ }),
))
```

## 运行

```sh
go test -race -count=1 ./...      # 竞态检测
go run ./cmd/demo                 # 运行样例
```

## 文件

- `pubsub/broker.go`：代理、订阅/发布/关闭编排。
- `pubsub/subscription.go`：订阅者有界队列、入队策略、幂等取消与收尾。
- `pubsub/handler.go`：注入任务、失败重试与退避。
- `pubsub/clock.go`：时钟抽象（系统时钟实现）。
- `pubsub/*_test.go`：竞态安全、背压隔离、显式丢弃、排空语义、假时钟重试等测试。
- `cmd/demo/main.go`：可运行样例。
