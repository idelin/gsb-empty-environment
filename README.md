# twocache：共享版本的两级缓存（L1/L2）

纯 Go 标准库实现的两级读缓存，演示**诚实的一致性语义**：L1/L2 共享版本水位，
读旧 L1 必须经失效通知 / 版本水位验证；L2 故障有明确降级规则；任何路径都不
假称强一致。数据源、时钟、故障全部可注入，无远程缓存依赖。

## 结构

| 文件 | 职责 |
|---|---|
| `cache.go` | `Cache`：读路径（L1→L2→source）、写路径、版本水位、失效循环、降级规则、`Close` 资源释放 |
| `l2.go` | `L2`：二级缓存，`GetErr`/`SetErr` 故障注入，写入拒绝版本回退 |
| `notifier.go` | `Notifier`：内存发布/订阅总线，`Delay`/`PublishErr` 注入通知延迟与丢失 |
| `source.go` | `Source`/`WritableSource` 接口与 `MapSource`（按 key 单调版本、`LoadErr` 注入） |
| `cmd/demo` | 双节点演示程序 |
| `cache_test.go` | 全部行为测试（`go test`） |

## 一致性声明（不夸大）

- **Strong**：只返回经版本水位验证、或从 L2/数据源重新取回的数据。L2 更新通知
  延迟时，强读**等待**水位追平（以 ctx 为界），超时就报错，绝不返回未验证数据。
- **Eventual**：从不等待通知，可直接用 L1 应答（通知未到时可能是旧值，这是最终
  一致的本义）；一旦水位证明 L1 已旧，则走 L2/数据源重新验证并收敛。只有在降级
  状态无法重新验证时，才返回 `Stale=true` 的旧值。

## L2 故障降级规则（明确、按序）

1. 直接回源：源可用则从源应答，`Degraded=true`（刷新 L1，跳过 L2）。
2. 源也不可用：**Strong 读必须失败**（`ErrL2Unavailable`），不静默降级。
3. Eventual 读可返回 L1 旧值，标记 `Stale=true, Degraded=true`；L1 也没有才报错。

降级结果永远不会被标记为强一致。

## 关键不变量

- 失效消息按 `(Src, Seq)` 去重，**重复投递幂等**（`Stats.DuplicateDropped` 计数）。
- **版本回退一律拒绝**（`ErrVersionRollback`）：失效消息、L2 写入、源加载三层都校验。
- 资源释放可验证：`Close` 退订通知总线并等待后台循环退出，
  `Notifier.SubscriberCount()` 归零；关闭后的读写返回 `ErrClosed`。
- 可注入点：`Source`（数据源）、`Clock`（时钟，控制 StrongTTL 复验窗口）、
  `L2.GetErr/SetErr`、`Notifier.Delay/PublishErr`（故障与延迟）。

## 运行样例

```console
$ go run ./cmd/demo
== 1. write on nodeA + strong read on nodeA ==
  strong: value="alice" version=1 from=L2 stale=false degraded=false
== 2. nodeB writes; notification delayed: eventual is declared-stale, strong waits ==
  nodeA eventual (notification in flight): value="alice" version=1 from=L1 stale=false degraded=false
  ...nodeA strong read is waiting for the invalidation notification...
  nodeA strong (after notification): value="alice-v2" version=2 from=L2 stale=false degraded=false
  nodeA eventual (converged after invalidation): value="alice-v2" version=2 from=L1 stale=false degraded=false
== 3. duplicate invalidation is idempotent, rollback rejected ==
  rollback apply error: cache: version rollback rejected
  nodeA stats: {3 1 1}
== 4. L2 failure: explicit degradation, never fake strong ==
  [event] L2 get failed for "user:1": L2 connection refused (degraded path)
  strong, L2 down (source answers): value="alice-v2" version=2 from=source stale=false degraded=true
  [event] L2 get failed for "user:1": L2 connection refused (degraded path)
  strong, L2+source down: cache: L2 unavailable, cannot serve requested consistency: source timeout
  [event] L2 get failed for "user:1": L2 connection refused (degraded path)
  eventual, L2+source down (declared stale): value="alice-v2" version=2 from=L1 stale=true degraded=true
== 5. resource release ==
  subscribers before close: 2
  subscribers after close:  0
  get after close: cache: closed
```

## 测试结果

`go test ./... -race -count=1 -v`，12 个测试全部通过：

```console
=== RUN   TestStrongReadValidatesStaleL1
--- PASS: TestStrongReadValidatesStaleL1 (0.01s)
=== RUN   TestEventualReadDoesNotWaitForNotification
--- PASS: TestEventualReadDoesNotWaitForNotification (0.05s)
=== RUN   TestEventualReadConvergesAfterInvalidation
--- PASS: TestEventualReadConvergesAfterInvalidation (0.01s)
=== RUN   TestStrongReadWaitsForDelayedNotification
--- PASS: TestStrongReadWaitsForDelayedNotification (0.17s)
=== RUN   TestDuplicateInvalidationIdempotent
--- PASS: TestDuplicateInvalidationIdempotent (0.00s)
=== RUN   TestDedupIsScopedPerPublisher
--- PASS: TestDedupIsScopedPerPublisher (0.00s)
=== RUN   TestVersionRollbackRejected
--- PASS: TestVersionRollbackRejected (0.00s)
=== RUN   TestL2FailureDegradation
--- PASS: TestL2FailureDegradation (0.00s)
=== RUN   TestStrongTTLWithInjectedClock
--- PASS: TestStrongTTLWithInjectedClock (0.01s)
=== RUN   TestInvalidationLoopViaBus
--- PASS: TestInvalidationLoopViaBus (0.01s)
=== RUN   TestWriteRejectedOnNonWritableSource
--- PASS: TestWriteRejectedOnNonWritableSource (0.00s)
=== RUN   TestCloseReleasesResources
--- PASS: TestCloseReleasesResources (0.00s)
PASS
ok  	example.com/twocache	2.073s
```

测试与需求的对应关系：

| 需求 | 测试 |
|---|---|
| 读旧 L1 需经水位/失效通知验证 | `TestStrongReadValidatesStaleL1`、`TestEventualReadConvergesAfterInvalidation` |
| 通知延迟：按声明一致性返回或等待 | `TestEventualReadDoesNotWaitForNotification`、`TestStrongReadWaitsForDelayedNotification` |
| L2 故障降级规则、不假称强一致 | `TestL2FailureDegradation` |
| 失效消息重复幂等 | `TestDuplicateInvalidationIdempotent`、`TestDedupIsScopedPerPublisher`、`TestInvalidationLoopViaBus` |
| 版本回退拒绝 | `TestVersionRollbackRejected` |
| 时钟/数据源/故障可注入 | `TestStrongTTLWithInjectedClock`、`TestL2FailureDegradation`、`TestWriteRejectedOnNonWritableSource` |
| 资源释放验证 | `TestCloseReleasesResources` |
