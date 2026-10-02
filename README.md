# timingwheel

离散时钟驱动的分层异步定时器轮（Hierarchical Timing Wheel），纯 Go 标准库实现。

## 设计

- **分层时间轮**：6 层轮，每层 64 个槽位。第 i 层每 `64^i` 个基础 tick 前进一格，
  覆盖约 `64^6 ≈ 6.9e10` tick 的到期范围。
- **O(1) 添加**：按到期 tick 直接定位到所在层与槽位（`deadline / tickSize % 64`），
  尾插双向链表。
- **O(1) 取消**：`Timer` 持有链表元素指针，`Cancel` 原子标记后直接 `list.Remove`，
  并释放槽位与任务引用。
- **离散推进**：调度器不持有 goroutine、不依赖墙上时钟。`Advance()` / `AdvanceTo(tick)`
  驱动时钟前进，到期任务严格在其 deadline tick 同步触发，完全确定、可复现。
- **自动降级（cascade）**：高层轮进入新槽位时，槽内定时器被重新插入更低层
  （更细粒度）的轮，逐层下沉直至第 0 层精确触发。
- **单次与周期**：`AfterFunc` 单次触发；`TickFunc` 周期 Ticker，按
  `deadline += period` 重排，无漂移，`Cancel` 即停。

## API

```go
tw := timingwheel.New(0)                       // 离散时钟从 tick 0 开始
t := tw.AfterFunc(5, func(now int64) { ... })  // 单次：第 5 个 tick 触发
tk := tw.TickFunc(2, func(now int64) { ... })  // 周期：每 2 个 tick 触发
t.Cancel()                                     // O(1) 取消，零触发
tw.Advance()                                   // 前进 1 tick
tw.AdvanceTo(1000)                             // 跳到第 1000 tick
tw.Now(); tw.Active()                          // 当前 tick / 存活定时器数
```

## 运行样例

```sh
go run ./cmd/demo
```

输出：

```
scheduled: A@3, B@9, ticker every 2, cancelled@5 (already cancelled)
[tick  2] ticker fired       (period 2)
[tick  3] one-shot A fired   (deadline 3)
[tick  4] ticker fired       (period 2)
[tick  6] ticker fired       (period 2)
[tick  8] ticker fired       (period 2)
[tick  8] ticker cancelled
[tick  9] one-shot B fired   (deadline 9)
done: now=12 active=0
```

被提前取消的 `cancelled@5` 零触发；ticker 在 tick 8 取消后不再触发；
结束时 `active=0`，内存完全释放。

## 测试

```sh
go test ./... -v
```

结果：

```
=== RUN   TestOneShotAlignment   # 100 个不等到期定时器，逐 tick 推进 1000 tick，全部精确触发
--- PASS: TestOneShotAlignment (0.00s)
=== RUN   TestCancel             # 提前/临期取消的任务零触发，其余精确触发，内存释放
--- PASS: TestCancel (0.00s)
=== RUN   TestTicker             # 周期 7 的 Ticker 在 1000 tick 内精确触发 142 次，取消后停止
--- PASS: TestTicker (0.00s)
=== RUN   TestJumpAdvance        # 单次跳跃 1000 tick，仍按 deadline 顺序精确触发
--- PASS: TestJumpAdvance (0.00s)
=== RUN   TestCascading          # 跨层降级：1..1000000 tick 的深层 deadline 精确触发
--- PASS: TestCascading (0.01s)
=== RUN   TestImmediate          # delay<=0 立即同步执行
--- PASS: TestImmediate (0.00s)
PASS
ok  	timingwheel	0.686s
```

每个用例结尾断言 `Active()==0` 且所有轮槽位为空（`bucketed()==0`），
验证取消/触发后的内存完全释放。
