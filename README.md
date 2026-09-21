# bounded resource pool (Go, 标准库)

一个并发安全的有限资源池。资源创建慢且可能失败；池限制的是 **创建中 + 使用中**
的资源总量，空闲资源不占配额。仅使用 Go 标准库（`sync`、`sync/atomic`、
`context`），无第三方依赖。

## 语义

- `New(Config{MaxOpen, Create, Close})`：`MaxOpen` 限制“创建中的数量 + 已借出
  数量”，空闲资源不计入。
- `Acquire(ctx)`：
  - 优先复用空闲资源；否则在配额允许时调用注入的 `Create(ctx)`。
  - 工厂收到派生 context：`ctx` 取消会取消创建中的工厂；`Acquire` 立即返回
    `ctx.Err()`，**不会等待慢创建**。
  - 若“取消”与“创建成功返回”竞争（工厂忽略取消仍造出资源），该资源会被
    `Close` 回收销毁，既不泄漏到后台，也不会放入空闲池复用。
  - 创建失败：错误返回给调用方，创建配额立刻释放，后续获取不受影响。
  - 池已关闭时返回 `pool.ErrClosed`。
- `Release()`：
  - 健康资源交给最久等待者，或放入空闲池复用。
  - `MarkBad()` 标记的坏资源归还时销毁，不复用。
  - 重复归还返回 `pool.ErrDoubleRelease`；被 `CloseForce` 回收后再归还返回
    `pool.ErrClosed`。
- `Close()`（优雅关闭）：拒绝新获取，等待所有借出资源归还、所有创建完成后，
  销毁全部资源；不会中途抛弃创建。
- `CloseForce()`（显式强制策略）：取消所有创建中的工厂，立即销毁空闲资源并
  回收仍在借出的资源，不等待借用方。

## 结构

- `pool/pool.go`：池实现（`sync.Mutex` + `sync.Cond`，句柄状态用
  `atomic.Int32`，取消唤醒用 `context.AfterFunc`）。
- `pool/pool_test.go`、`pool/harness_test.go`：基于 `go test` 的测试。
  工厂函数、创建耗时、失败、可阻塞门控全部由测试注入。
- `pool/example_test.go`：可由 `go test` 运行的 `ExamplePool`。
- `cmd/demo`：可运行样例（复用、取消慢创建、关闭）。

## 运行

```bash
go test ./... -race -count=1 -v   # 竞态检测
go run ./cmd/demo                 # 运行样例
```

## 覆盖的关键场景

- 配额只计“创建中 + 使用中”，高并发下不超过 `MaxOpen`。
- 创建失败释放配额；排队等待者可被后续释放唤醒。
- 创建过程中取消：工厂 ctx 被取消，无后台资源残留。
- 取消与创建完成竞争（300 次）：资源被销毁而非交付/复用。
- 取消 100 次（配额为 1）：不留 100 个后台资源，`created == closed`。
- 坏资源销毁；重复归还报错；`Close` 等待归还；`CloseForce` 立即回收。
- 64 goroutine × 25 轮混合成功/失败/坏资源压测：无泄漏、无超限。
