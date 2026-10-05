# fairlock — 公平、可取消、可降级的读写锁

`fairlock` 提供带以下性质的 `RWMutex`：

- **读 / 写 / 写转读降级**：`Lock`、`RLock`、`Downgrade`（写锁原子转为读锁）。
- **公平**：等待者按 FIFO 授予；一旦有写者排队，后来的读者排在其后，
  写者不会被读者流无限推迟（无写者饥饿）。
- **等待可取消**：`Lock(ctx)` / `RLock(ctx)` 在 ctx 结束时返回 `ctx.Err()`。
- **降级无间隙**：`Downgrade` 在内部互斥锁的同一个临界区内完成
  `writing=false; readers++`，任何观察者都不可能看到锁处于完全未持有状态。

```go
var mu fairlock.RWMutex

if err := mu.Lock(ctx); err != nil { return err } // 写锁
mu.Downgrade()                                     // 原子转为读锁，无间隙
mu.RUnlock()                                       // 降级后用 RUnlock 释放
```

误用（未持有时 `Unlock` / `RUnlock` / `Downgrade`）会 panic，与 `sync` 包一致。

## 状态机

```
writing bool      // 写令牌是否在外
readers int       // 在读份额数
queue   []*waiter // FIFO 等待队列
```

所有状态迁移都在内部互斥锁 `mu` 下完成。调度器 `scheduleLocked` 的不变式：

- 队首是写者：仅当 `!writing && readers == 0` 时授予，授予后停止（写独占）；
- 队首是读者：连续授予队首的整个读者段（读者可共存），遇到写者或
  `writing` 停止；
- 每次可能使队首变为可授予的状态迁移（`Unlock`、`RUnlock` 至 0、
  `Downgrade`、取消出队）之后，**在同一临界区内**立即运行调度器。

## 授予与取消同到时的胜负规则

每个授予和每个取消都在 `mu` 下**线性化**：

- **授予**的线性化点：调度器在 `mu` 下设 `waiter.granted = true`（同时把
  waiter 出队、向容量为 1 的 `token` channel 发送令牌）；
- **取消**的线性化点：等待者的取消路径在 `mu` 下发现 `granted == false`
  并将其出队。

**先线性化者胜。** 两个事件被同一把互斥锁串行化，因此恰好一方获胜，
不存在"既授予又取消"或"两者都丢"的中间态：

- 授予先：取消路径看到 `granted == true`，返回 `nil`——调用者持有锁，
  必须正常释放；
- 取消先：等待者被移出队列，返回 `ctx.Err()`，并在同一临界区内重跑
  调度器。

补充规则：若锁可立即授予（快路径），即使 ctx 已取消也获取成功——
无竞争的获取不会失败。

## 证明概要

记事件按 `mu` 的获取顺序线性化，对线性化序列做归纳。

**引理 1（写互斥，不会授予两名写者）。** 写令牌只在 `scheduleLocked`
中、观察到 `writing == false && readers == 0` 时创建，且在同一临界区内
置 `writing = true`；只在 `Unlock` / `Downgrade` 中于 `mu` 下销毁。
创建前检查与置位原子（同在 `mu` 下），故任意时刻写令牌数 ≤ 1。

**引理 2（读写互斥）。** 读者授予要求 `!writing`；写者授予要求
`readers == 0`。两个检查与对应的状态置位都在 `mu` 下原子完成，
故 `writing ⇒ readers == 0` 且 `readers > 0 ⇒ !writing`。

**引理 3（不遗失唤醒）。** 令 waiter `w` 在队列中且队首可授予。调度器
在每次相关状态迁移后的同一临界区内运行（不变式见上），故 `w` 必将被
授予。授予时：(a) `token` channel 容量为 1，发送不阻塞；(b) `w` 同时被
出队，不会重复授予；(c) 若 `w` 的取消路径已先执行，则它必在 `mu` 下
看到 `granted == false`——与"已授予"矛盾；若取消路径尚未执行，它之后
会看到 `granted == true` 并兑现该授予。因此每个授予要么被正常消费，
要么被取消路径显式兑现，令牌不会丢失。反过来，取消出队后同一临界区内
重跑调度器，后继者可授予时立即被唤醒，取消本身也不制造遗失唤醒。

**引理 4（降级无间隙）。** `Downgrade` 在单个 `mu` 临界区内执行
`writing = false; readers++`。写者授予需要调度器在 `mu` 下观察到
`readers == 0`；而 `readers` 从 0 变 1 与 `writing` 从 1 变 0 之间没有
任何可观察的中间状态（两者同属一个临界区）。故不存在锁完全空闲的瞬间。

**定理（无写者饥饿）。** 写者 `W` 在位置 `k` 入队后：快路径要求队列
为空，故新读者不能插队；调度器只从队首连续授予，故 `W` 之前的读者段
有限，且 `W` 之后入队的读者永远排在 `W` 之后。`W` 只等待：当前活跃
读者 + 队列中先于它的读者，二者数量在 `W` 入队时已确定。假设持锁者
最终释放，则 `W` 的等待有界。

## 确定性调度测试

`rwmutex_test.go` 不依赖时序运气：测试通过轮询内部队列长度
（`waitQueueLen`）建立 goroutine 间的 happens-before 边，把并发执行
钉成唯一的相关交错，再断言结果：

| 测试 | 固定的调度 | 证明的性质 |
|---|---|---|
| `TestWriterNotStarvedByLaterReaders` | R 持有 → W 排队 → R2 排队 → 释放 | 后来的读者不插队，写者优先 |
| `TestReaderBatchBeforeWriter` | W 持有 → R1,R2 排队 → W2 排队 → 释放 | 队首读者段一起授予，写者等待 |
| `TestDowngradeHasNoGap` | W 持有 → W2 排队 → 降级 | 降级后 W2 不可授予（无间隙） |
| `TestCancelHeadWriterNoLostWakeup` | W0 持有 → W1,W2 排队 → 取消 W1 → 释放 | 取消队首不遗失 W2 的唤醒 |
| `TestCancelHeadReaderUnblocksWriter` | W 持有 → R1,W2 排队 → 取消 R1 → 释放 | 同上（读者版本） |
| `TestGrantWinsWhenLinearizedFirst` | W0 持有 → W1 排队 → 释放 → 取消 | 授予先线性化 ⇒ 授予胜 |
| `TestCancelWinsWhenLinearizedFirst` | W0 持有 → W1 排队 → 取消 → 释放 | 取消先线性化 ⇒ 取消胜 |
| `TestStressExclusion` | 随机交错（`-race`） | 写互斥、读写互斥、降级正确性 |

运行：

```sh
go test -race -count=5 ./...
```
