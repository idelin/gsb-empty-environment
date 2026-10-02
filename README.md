# weakbus — 弱引用响应式事件总线与内存泄漏探测器

基于弱引用语义的响应式发布/订阅中心，仅使用 Go 标准库。
在 Go 中以 `runtime.SetFinalizer` 模拟 JS 的 `WeakRef` /
`FinalizationRegistry`：订阅者（组件）被销毁且无外部强引用后，GC 回收
订阅句柄，事件总线自动注销其回调；内置探测器主动发现遗留的僵尸订阅者
并输出告警，杜绝跨页面/跨组件内存泄漏。

## 设计

- `Bus.Subscribe(topic, handler) *Subscription` — 总线**不持有**句柄的
  强引用，句柄生命周期完全由订阅者控制（WeakRef 语义）。
- 句柄不可达时，finalizer 将监听器标记为 dead 并进入待清理队列
  （FinalizationRegistry 回调队列语义），不直接改动主题表。
- `Bus.Sweep()` / `Bus.StartWatchdog(interval)` — 两阶段清理的第二阶段：
  移除已死监听器；滞留超过 `WithZombieTimeout` 的条目判定为僵尸，
  输出 `WARN zombie subscription detected` 告警并强制注销。
- `Subscription.Unsubscribe()` — 确定性立即注销（幂等），并解除 finalizer。
- `Bus.Stats()` — `Active` / `Pending` / `Cleaned` 计数快照。

## 运行

```sh
go test -v ./...   # 运行验收测试
go run ./cmd/demo  # 运行演示
```

## 验收边界（bus_test.go）

`TestAutoCleanupAfterGC_MonotonicToZero`：组件 A 订阅全局事件流后被销毁
（句柄无任何强引用），触发 GC 后总线自动清理无效监听器，断言活跃订阅者
计数**单调递减归零**（采样序列 `[1 0]`）。

其他用例：watchdog 自动清扫、僵尸告警与强制注销、显式退订幂等且不误报、
存活订阅者不受清理影响。

## 测试结果

```
=== RUN   TestAutoCleanupAfterGC_MonotonicToZero
--- PASS: TestAutoCleanupAfterGC_MonotonicToZero (0.09s)
    bus_test.go:72: active count samples (monotonic to zero): [1 0]
=== RUN   TestWatchdogAutoSweep
--- PASS: TestWatchdogAutoSweep (0.11s)
=== RUN   TestZombieDetectionWarns
--- PASS: TestZombieDetectionWarns (0.16s)
=== RUN   TestExplicitUnsubscribe
--- PASS: TestExplicitUnsubscribe (0.13s)
=== RUN   TestLiveSubscribersUnaffected
--- PASS: TestLiveSubscribersUnaffected (0.15s)
PASS
ok  	weakbus
```

## 演示输出（go run ./cmd/demo）

```
component A subscribed (topic=global), active=1
component A received: hello, A
component A destroyed, active=1 (awaiting GC)
[eventbus] WARN zombie subscription detected: id=1 topic="global" dead for 115ms ...; force-unsubscribing
after GC: active=0 pending=0 cleaned=1
manual sweep: swept=1 zombies=1
final: active=0 pending=0 cleaned=2
OK: no leaked listeners — active subscriber count is zero
```
