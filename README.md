# memsched — 协程内存配额与 GC 协作调度器

纯 Go 标准库实现。调度器与内存监控器协作：统计每个 Fiber 执行过程中申请的
临时对象大小；全局内存吃紧时优先调度具备 GC 回收能力的协程，并对新协程
实施准入反压。

## 设计

- `Monitor`（monitor.go）：内存监控器。
  - `Alloc/Release` 精确统计每个 Fiber 的临时对象字节数与全局用量；
  - 硬配额：全局用量达到 `max` 时 `Alloc` 阻塞，峰值永不越限；
  - 反压滞回：用量 ≥ 80%（高水位）时 `Admit` 阻塞、拦截新协程创建；
    存量协程释放内存、用量回落至 60%（低水位）后才平滑恢复接单，
    避免在水位线附近抖动。
- `Scheduler`（scheduler.go）：协程池调度器。
  - `Submit` 先过准入门（反压期间阻塞调用方）；
  - 具备 GC 回收能力的协程进入优先队列，内存吃紧时被 worker 优先调度。
- `Fiber`（fiber.go）：受监控的最小执行单元，`Context.Alloc/Release`
  是协程申请临时对象的唯一入口。

## 运行样例

```console
$ go run ./cmd/demo
==== 协程内存配额与 GC 协作调度器 · 运行样例 ====
注入批处理任务:   100
完成协程数:       100
内存配额上限:     1000 (高水位 800 = 80%, 低水位 600 = 60%)
内存峰值:         914 (未越配额: true)
反压触发次数:     23 (达到 80% 阈值，拦截新协程创建)
平滑恢复次数:     23 (存量协程释放内存后恢复接单)
准入协程总数:     100
结束时内存占用:   0 (无泄漏: true)
总耗时:           43ms
```

## 测试结果

```console
$ go test -race -v ./...
=== RUN   TestBackpressureAndRecovery
    memsched_test.go:73: stats: {Used:0 Peak:1000 Max:1000 High:800 Low:600 BackpressureEvents:10 ResumeEvents:10 Admitted:100}
--- PASS: TestBackpressureAndRecovery (0.03s)
=== RUN   TestAdmissionHysteresis
--- PASS: TestAdmissionHysteresis (0.12s)
=== RUN   TestReclaimCapableFibersScheduledFirst
--- PASS: TestReclaimCapableFibersScheduledFirst (0.00s)
=== RUN   TestPerFiberAccountingAndQuota
--- PASS: TestPerFiberAccountingAndQuota (0.05s)
PASS
ok  	memsched	1.969s
```

- `TestBackpressureAndRecovery`：验收场景——注入 100 个批处理任务，
  80% 阈值触发反压并拦截新协程，存量释放后平滑恢复，峰值不越配额、无泄漏。
- `TestAdmissionHysteresis`：高水位拦截、低水位恢复的滞回语义。
- `TestReclaimCapableFibersScheduledFirst`：GC 回收能力协程优先调度。
- `TestPerFiberAccountingAndQuota`：每 Fiber 精确统计与硬配额阻塞。
