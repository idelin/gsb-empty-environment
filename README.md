# fiber — 确定性协作式轻量级协程调度器

单线程、单进程的离散事件协程（Fiber）调度器，仅使用 Go 标准库。

## 设计

- **协作式调度**：Fiber 只能通过 `Yield()` 主动让出控制权，或函数返回时退出。
  任意时刻只有一个 Fiber 在运行（通过无缓冲 channel 严格交接），从机制上消除并发不确定性。
- **轮转（Round-Robin）**：调度器按固定游标顺序在就绪队列中轮转，已退出（`Dead`）的 Fiber 被跳过。
- **虚拟离散时钟**：每次分派（dispatch）推进一个时间片 `SliceMicros`（默认 1µs），
  事件日志中的 tick 序列逐微秒完全可复现。
- **确定性**：同一工作负载无论运行多少次，事件序列（SPAWN/RESUME/YIELD/EXIT 与 tick）
  逐条同构。`TestDeterministicReproducible` 连续运行 200 次逐事件比对验证。

## API

```go
s := &fiber.Scheduler{SliceMicros: 1}
s.Spawn("A", func(f *fiber.Fiber) {
    f.Yield() // 挂起，让出控制权
    // 被恢复后继续执行
})          // 创建（SPAWN）
s.Run()     // 运行至所有 Fiber 退出
log := s.Log() // 确定性调度日志
```

## 运行样例

```
$ go run ./cmd/demo
scheduling log (run #1):
  tick=000000us event=SPAWN  fiber=0
  tick=000000us event=SPAWN  fiber=1
  tick=000000us event=SPAWN  fiber=2
  tick=000001us event=RESUME fiber=0
  tick=000001us event=YIELD  fiber=0
  tick=000002us event=RESUME fiber=1
  tick=000002us event=YIELD  fiber=1
  ...
  tick=000012us event=RESUME fiber=2
  tick=000012us event=EXIT   fiber=2

run #2 identical to run #1 (tick-by-tick): true
```

3 个协程严格按 A→B→C→A→B→C… 交替执行，每次分派恰好消耗 1µs 虚拟时间。

## 测试结果

```
$ go test -v ./...
=== RUN   TestAlternatingYield
--- PASS: TestAlternatingYield (0.00s)
=== RUN   TestDeterministicReproducible
--- PASS: TestDeterministicReproducible (0.01s)
=== RUN   TestVirtualClock
--- PASS: TestVirtualClock (0.00s)
=== RUN   TestFiberStates
--- PASS: TestFiberStates (0.00s)
=== RUN   TestExitWithoutYield
--- PASS: TestExitWithoutYield (0.00s)
=== RUN   TestUnevenLifetimes
--- PASS: TestUnevenLifetimes (0.00s)
PASS
ok      fiber
```

`go test -race ./...` 同样通过。
