# timingwheel

分层时间轮（Hierarchical Timing Wheel）定时器调度器，由离散时钟驱动，仅使用 Go 标准库。

## 特性

- **O(1) 添加与取消**：`After` / `Ticker` 将定时器插入对应层级槽位的哈希集合；`Cancel` 直接从集合中摘除。
- **离散时钟推进**：`Advance(n)` 逐 tick 推进时钟，到期任务在**严格对齐的 deadline tick** 同步触发。
- **分层自动降级**：远未来定时器存放在高层轮（overflow wheel），随时间推进自动逐级降级到低层轮，最终精确到期。
- **单次定时 + 周期 Ticker**：`After` 一次性触发；`Ticker` 按固定周期重复触发，可随时 `Cancel`。
- **取消零触发、内存完全释放**：取消的定时器立即从轮中摘除，永不触发；全部结束后轮中无任何残留。

## 结构

| 文件 | 说明 |
| --- | --- |
| `timingwheel.go` | 分层时间轮实现（`TimingWheel` / `Timer` / 层级 `wheel` / 槽位 `bucket`） |
| `timingwheel_test.go` | 全部验收测试（`go test`） |
| `cmd/example/main.go` | 运行样例 |

## 设计要点

- 第 0 层轮每个槽位 1 tick，共 `wheelSize` 槽；第 k 层轮的 tick 为 `wheelSize^k`，高层轮按需懒创建。
- 添加定时器时按 deadline 落入对应层级的槽位（每层 O(1)）。
- 每推进 1 tick，各层 `advanceClock` 检查当前槽位，到期槽位中的定时器被取出后**重新插入**（`add`）：仍未到期的降级到低层轮，已到期的立即触发——保证唤醒时刻与 deadline 严格一致。
- `Cancel` 将定时器从槽位 map 中 O(1) 删除并递减计数，内存随之释放。

## API

```go
tw := timingwheel.New(20)        // 底层轮 20 个槽位
t1 := tw.After(50, task)         // 50 tick 后触发一次
t2 := tw.Ticker(10, task)        // 每 10 tick 触发一次
t1.Cancel()                      // O(1) 取消，零触发
tw.Advance(1000)                 // 推进 1000 个 tick，同步触发到期任务
tw.Now()                         // 当前 tick
tw.Pending()                     // 存活定时器数量
```

## 运行样例

```sh
$ go run ./cmd/example
[tick   3] one-shot A fired
[tick   4] ticker fired
[tick   8] ticker fired
[tick  10] one-shot B fired
[tick  12] ticker fired
[tick  12] ticker cancelled
[tick 100] one-shot C fired (cascaded from overflow wheel)
done: now=100 pending=0 (all timers fired or cancelled, memory released)
```

## 测试结果

```sh
$ go test ./... -v
=== RUN   TestExactWakeup              # 100 个定时器，1000 tick，全部精确唤醒
--- PASS: TestExactWakeup (0.01s)
=== RUN   TestCancelZeroFires          # 取消的定时器零触发，内存释放
--- PASS: TestCancelZeroFires (0.00s)
=== RUN   TestCancelMidFlight          # 推进中途取消
--- PASS: TestCancelMidFlight (0.00s)
=== RUN   TestTicker                   # 周期 Ticker 精确对齐周期倍数
--- PASS: TestTicker (0.00s)
=== RUN   TestTickerCancelInsideTask   # 任务内部自我取消
--- PASS: TestTickerCancelInsideTask (0.00s)
=== RUN   TestHierarchicalCascade      # 多层级联降级（4 槽小轮，deadline 至 3000）
--- PASS: TestHierarchicalCascade (0.00s)
=== RUN   TestFiringOrder              # 触发顺序按 deadline 排序
--- PASS: TestFiringOrder (0.00s)
=== RUN   TestImmediateAfter           # delay<=0 立即执行
--- PASS: TestImmediateAfter (0.00s)
=== RUN   TestManyTimersStress         # 验收场景：100 定时器 / 1000 tick / 精确唤醒 / 取消零触发 / 内存释放
--- PASS: TestManyTimersStress (0.00s)
=== RUN   ExampleTimingWheel
--- PASS: ExampleTimingWheel (0.00s)
PASS
ok  	timingwheel	0.468s
```

> 注：沙箱环境中需指定可写的构建缓存，如 `GOCACHE=/tmp/gocache go test ./...`。
