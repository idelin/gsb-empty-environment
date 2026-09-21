# keysched — 同键保序、动态分片的任务调度器

纯 Go 标准库实现（`sync` / `context` / `hash/fnv`），不依赖任何第三方包。

## 语义

- **同键严格串行**：相同 key 的任务任意时刻最多一个在执行，按提交顺序完成。
- **异键并行**：不同 key 的任务散列到不同分片并行执行；单热键只占用自己的分片，不阻塞其他分片。
- **动态分片扩缩容仍保序**：`Reshard(n)` 运行期调整分片数。每个 key 的后继任务只在其前驱的完成路径上被派发（完成链式派发），因此在扩容前提交、扩容后执行的任务依然按序完成。
- **迁移安全交接**：`Reshard` 会等待上一代分片把"已派发未启动"的任务全部交接（drain）后才返回；在途任务在旧 worker 上自然完成，新派发一律走新一代分片。
- **取消不打乱剩余顺序**：取消未启动任务会从键队列中摘除，其余任务相对顺序不变；取消运行中任务会取消其 `context`，结果记为 `ErrCanceled`，键的后续链条照常推进。
- **空队列回收**：键队列清空且无在途任务时即回收条目；回收与新建都在同一把互斥锁内完成，不会与并发入队竞态丢任务。

## 关键不变式

> 若某 key 有待执行任务，则该 key 恰有一个任务在途（已派发或运行中）；
> 在途任务完成时在调度器锁内把下一个任务派发到**当前代**分片。

由此：每 key 的并发度恒为 1，顺序由完成链保证，与分片代数无关；派发只读当前代
`shards`，旧分片只消化存量，交接天然安全。分片内部是无界队列 + `sync.Cond`，
`push` 永不阻塞，调度器锁内派发不会与 worker 的完成回调形成死锁。

## 布局

- `scheduler/scheduler.go` — 调度器实现（`Scheduler` / `Future` / `Reshard` / `Close`）
- `scheduler/scheduler_test.go` — 注入式测试（任务函数、时序、失败均由测试注入）
- `cmd/demo/main.go` — 运行样例：提交 → 扩缩容 → 取消 → 注入失败 → 打印执行轨迹

## 运行

```sh
go test -race -count=1 -v ./...   # 竞态检测 + 全部测试
go run ./cmd/demo                 # 运行样例
```

## 测试结果（go test -race）

```
=== RUN   TestSameKeyStrictlySerial
--- PASS: TestSameKeyStrictlySerial (0.23s)          # 同键 200 任务：并发度恒 1、顺序 0..199
=== RUN   TestDifferentKeysParallel
--- PASS: TestDifferentKeysParallel (0.00s)          # 4 键 4 分片同时起跑（屏障验证）
=== RUN   TestReshardPreservesOrderAcrossGenerations
--- PASS: TestReshardPreservesOrderAcrossGenerations # 2→8→1→16 扩缩容前后提交仍按序完成
=== RUN   TestReshardWaitsForHandoff
--- PASS: TestReshardWaitsForHandoff (0.10s)         # 旧分片有未启动任务时 Reshard 不返回
=== RUN   TestCancelPendingKeepsOrder
--- PASS: TestCancelPendingKeepsOrder (0.00s)        # 取消每第 3 个任务，剩余顺序不变
=== RUN   TestCancelRunningTask
--- PASS: TestCancelRunningTask (0.00s)              # 运行中取消：ctx 取消 + ErrCanceled，链条继续
=== RUN   TestEmptyQueueReclaimRace
--- PASS: TestEmptyQueueReclaimRace (0.02s)          # 8 goroutine × 300 次同键入队/回收，零丢失
=== RUN   TestHotKeyDoesNotBlockOtherShards
--- PASS: TestHotKeyDoesNotBlockOtherShards (0.00s)  # 热键驻留期间其他分片任务照常完成
=== RUN   TestFailureInjection
--- PASS: TestFailureInjection (0.00s)               # 失败只影响自身 Future，链条顺序不变
=== RUN   TestCloseDrains
--- PASS: TestCloseDrains (0.02s)                    # Close 排空全部任务，之后提交返回 ErrClosed
PASS
ok  	keysched/scheduler	1.503s
# 重复 5 轮：ok  keysched/scheduler  2.998s（-race -count=5）
```

## 样例输出（go run ./cmd/demo，节选）

```
submitted 24 tasks on 4 keys, shards=2
resharding 2 -> 8 (scale up) ...
resharding 8 -> 3 (scale down) ...
canceling order-3#4 ...

results:
  order-2#3 -> FAILED: injected failure     # 注入的失败只落在自己身上
  order-3#4 -> CANCELED                     # 取消不影响 order-3#3/#5 的顺序
  ...其余 22 个全部 ok，且每个 key 内序号严格递增...

execution trace (as observed):
  execute order-2#0 (shard=1 epoch=2)       # 全部任务在扩缩容之后的新代执行
  execute order-3#0 (shard=0 epoch=2)
  ...同一 key 的序号在执行轨迹中严格递增，跨 key 交错并行...

done: shards=3 epoch=2 live-key-queues=0    # 空队列全部回收
```
