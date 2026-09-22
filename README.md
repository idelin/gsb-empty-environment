# keyq — 分片键序任务调度器

仅用 Go 标准库实现的键控任务队列：

- **同键严格串行**：同一 key 的任务按提交顺序逐个执行，绝不并发。
- **异键并行**：不同 key 按哈希分散到各分片（shard）并行执行。
- **动态扩缩容保序**：`Rescale(n)` 在线调整分片数；在途任务在旧分片上安全交接（执行完毕）后，后续任务才按新分片路由，键内顺序不受扩容/缩容影响。
- **取消不打乱顺序**：`Handle.Cancel()` 仅移除目标任务（未开始时）或协作式取消运行中任务（context），其余任务顺序不变。
- **空队列回收安全**：键状态在队列空且无在途任务时回收，回收与新入队由单调度协程串行化，无竞态丢任务。
- **热键不阻塞其他分片**：键级串行只约束本键，其他分片上的键照常推进。

## 设计

所有调度状态（每键 FIFO 队列、在途标记、分片忙闲）由**单个 dispatcher 协程**持有，
通过 channel 与外界通信，因此扩缩容时键队列无需迁移——只有 worker 在新旧分片间交接，
交接点等待在途任务完成，天然保序。核心状态零锁。

```
Submit/Cancel/Rescale/Shutdown ──channel──> dispatcher goroutine
                                              │  per-key FIFO + in-flight
                                              ▼
                                     shard-0  shard-1  ...  shard-N (worker goroutines)
```

## API

```go
s := keyq.New(keyq.WithShards(4))          // 可选 keyq.WithHashFunc 注入哈希（测试用）
h := s.Submit("user-42", func(ctx context.Context) error { ... })
s.Rescale(8)                                // 动态扩缩容，返回时新分片组已生效
h.Cancel()                                  // 取消排队任务 / 协作取消运行任务
err := h.Wait()                             // 等待结果（含注入的失败）
s.Shutdown(ctx)                             // 排空并停止
```

## 运行

```sh
go test -race -v ./...   # 竞态检测下的全部测试
go run ./cmd/demo        # 运行样例
```

## 测试覆盖（任务函数、时间、失败均由测试注入）

| 测试 | 验证点 |
| --- | --- |
| `TestSameKeyStrictOrder` | 同键 200 任务严格按序、并发度恒为 1 |
| `TestDifferentKeysRunInParallel` | 异键跨分片并行（否则会死锁超时） |
| `TestRescalePreservesOrderExpandAndShrink` | 扩容 2→8、缩容 8→1 期间及之后同键仍按序 |
| `TestMigrationWaitsForInflightHandoff` | 迁移等待在途任务安全交接，后续任务不抢跑 |
| `TestCancelDoesNotReorderRemaining` | 取消中间任务，剩余顺序不变 |
| `TestCancelRunningTaskIsCooperative` | 运行中任务通过 context 协作取消 |
| `TestEmptyQueueReclaimDoesNotLoseTasks` | 多协程反复入队/排空，回收不丢任务（-race） |
| `TestHotKeyDoesNotBlockOtherShards` | 热键长任务不阻塞其他分片 |
| `TestInjectedFailureDoesNotStopQueue` | 注入失败不影响同键后续任务 |
| `TestSubmitAfterShutdownRejected` | 关闭后提交被拒绝 |
| `TestStressMixedKeysUnderRescale` | 8 键 × 100 任务在 20 次连续扩缩容下各自保序 |
