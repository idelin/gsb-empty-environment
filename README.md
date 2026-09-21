# lease — 可撤销读写租约锁

仅使用 Go 标准库（`sync.Mutex` + 通道）实现的读写租约锁。

## 特性

- **写者优先排队**：写者进入 FIFO 队列后即阻断新读者，持续读流下写者不会饥饿。
- **读升级写**：`Upgrade` 在排队期间临时让出租约计数；两个读者同时升级会先后
  获得写锁，不会互相永久等待。升级失败（如超时）时原读租约自动恢复。
- **租约令牌**：每次授予返回带代际号的 `Token`；重复释放返回
  `ErrDoubleRelease`，跨代（撤销后）释放返回 `ErrStaleToken`，且不触碰锁计数。
- **可撤销**：`Revoke` 使代际号 +1、作废旧租约，并执行注入的撤销任务；
  写租约可设 TTL，到期自动撤销。
- **依赖注入**：时钟（`WithClock`）、撤销任务（`WithRevokeHook`）、写租约
  TTL（`WithWriteTTL`）均由调用方注入，失败可经钩子返回值注入。

## 用法

```go
l := lease.New(lease.WithWriteTTL(time.Minute))
rt, _ := l.AcquireRead(ctx)          // 读租约
wt, _ := l.Upgrade(ctx, rt)          // 升级为写租约；失败时 rt 仍有效
_   = l.Release(wt)                  // 释放；再次释放返回 ErrDoubleRelease
```

## 运行

```sh
go test -race -v ./...   # 测试（含竞态检测）
go run ./cmd/demo        # 运行样例
```
