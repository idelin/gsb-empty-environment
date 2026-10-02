# weakbus — 弱引用响应式事件总线与内存泄漏探测器

基于 Go 1.24 `runtime.AddCleanup`（语义等价于 JS 的 `WeakRef` /
`FinalizationRegistry`）实现的响应式发布订阅中心。订阅与组件（owner）
的生命周期绑定：组件被垃圾回收时，运行时的清理回调自动把订阅从总线
移除，杜绝跨页面/跨模块的僵尸监听器与内存泄漏。内置泄漏探测器主动
扫描遗留订阅并输出告警。

仅使用标准库。要求 Go ≥ 1.24。

## 结构

- `eventbus/bus.go` — 事件总线：`Subscribe`（弱引用，自动注销）、
  `SubscribeStrong`（手动注销）、`Publish`、订阅计数与快照。
- `eventbus/leak.go` — `LeakDetector`：强制 GC、用哨兵对象等待清理
  队列排空，然后把存活时间超过 `maxAge` 的订阅标记为僵尸并告警；
  `Monitor` 可后台周期扫描。
- `eventbus/bus_test.go` — 验收测试（见下）。
- `cmd/demo/main.go` — 运行样例。

## 用法

```go
bus := eventbus.New()
detector := eventbus.NewLeakDetector(bus, logger, 100*time.Millisecond)

type Component struct{ name string }
c := &Component{name: "Header"}

// 订阅生命周期绑定到 c；c 被 GC 后订阅自动移除。
sub := eventbus.Subscribe(bus, "global", c, func(ev eventbus.Event) {
    // 注意：不要在回调里强引用 c，否则 c 永远无法被回收。
})

bus.Publish("global", "page-refresh")
sub.Unsubscribe()        // 也可以主动注销（幂等）
report := detector.Scan() // 主动探测僵尸订阅并告警
```

## 验收测试

`TestAutoCleanup_SubscriberCountMonotonicToZero` 对应验收边界：
组件 A 订阅全局事件流后被销毁且无外部强引用，强制 GC 后总线自动
清理内部监听器，采样断言活跃订阅者计数**单调递减并归零**。其余测试
覆盖：GC 后不再投递给死订阅者、手动注销幂等、探测器对泄漏组件告警、
干净总线无误报。

```console
$ go test ./eventbus/ -v
=== RUN   TestAutoCleanup_SubscriberCountMonotonicToZero
    bus_test.go:85: subscriber count decayed monotonically to zero: [1 0]
--- PASS: TestAutoCleanup_SubscriberCountMonotonicToZero (0.03s)
=== RUN   TestPublish_DeadSubscribersNotInvoked
--- PASS: TestPublish_DeadSubscribersNotInvoked (0.03s)
=== RUN   TestUnsubscribe_Eager
--- PASS: TestUnsubscribe_Eager (0.00s)
=== RUN   TestLeakDetector_WarnsOnZombies
    bus_test.go:176: detector warning: WARNING: zombie subscriber detected: topic="global" id=1 age=139ms (possible leaked component)
--- PASS: TestLeakDetector_WarnsOnZombies (0.15s)
=== RUN   TestLeakDetector_NoFalsePositives
--- PASS: TestLeakDetector_NoFalsePositives (0.05s)
PASS
ok  	weakbus/eventbus
```

## 运行样例

```console
$ go run ./cmd/demo
--- phase 1: components subscribe, then are destroyed ---
active subscribers after mount: 3
forcing GC; waiting for automatic deregistration...
active subscribers: 0
delivered to 0 handlers after teardown (want 0)
--- phase 2: leak detection ---
[eventbus] WARNING: zombie subscriber detected: topic="global" id=4 age=177ms (possible leaked component)
scan report: active=1 zombies=1
```

## 实现要点

- **自动注销**：`runtime.AddCleanup(owner, cleanup, arg)` 在 owner
  不可达后由运行时专用 goroutine 执行清理；`arg` 只携带总线指针与
  订阅 ID，绝不引用 owner，否则 owner 会被永久钉活。
- **排空检测**：清理回调异步执行，且不一定在入队的那个 GC 周期内
  跑完。探测器用一个带清理回调的哨兵对象确认"一整轮清理已排空"，
  并要求计数连续两个已排空周期不变才判定稳定。
- **tiny allocator 陷阱**：小于 16 字节的对象共享分配块，块内其他
  对象存活时清理回调不会触发。owner 应使用普通堆对象（真实组件
  一般都大于 16 字节）。
