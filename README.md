# causalcast — 向量时钟因果广播（单进程事件模拟器）

纯 Go 标准库实现。消息携带向量时钟，节点只有在全部因果前驱交付后才交付消息；
成员集合固定；重复消息去重；缓存超限返回显式背压错误。通信、故障与时间全部由
单进程离散事件模拟器 `Sim` 控制。

## 结构

- `vector.go` — `VectorClock`（克隆、按分量取 max 合并）
- `message.go` — `Message{ID{Sender,Seq}, Clock, Payload}`
- `node.go` — `Node`：广播、因果就绪判定、hold-back 缓存、去重、`ErrBackpressure`
- `order.go` — `VerifyLog`（安全性验证器）、`CanonicalOrder`（并发消息稳定排序）
- `sim.go` — `Sim`：事件堆、网络时延、丢包/重复注入、背压重传、逻辑时钟
- `cmd/demo` — 运行样例

## 交付规则（安全性核心）

对来自 s 的消息 m，节点交付时钟为 D：

```
m.Clock[s] == D[s] + 1        （s 的下一个序号）
m.Clock[k] <= D[k]  (k != s)  （全部因果前驱已交付）
```

不满足则进入 hold-back 缓存；缓存满返回 `ErrBackpressure`（消息不被接受，
由调用方/模拟器稍后重传）。**不存在任何超时交付路径**：缺前驱的消息永远留在
缓存里，不会被"超时后冒充已满足"。

## 证据

安全性（`node_test.go`, `sim_test.go`）：

- `TestCausalDependencyOrder` — B 收到 A 的消息后发出的 m2 带 A 的因果过去，
  C 先收到 m2 也只缓存不交付，m1 到达后才按 m1→m2 交付。
- `TestDeduplication` — 已交付/缓存中的重复副本均被去重。
- `TestBackpressureSignal` — 缓存满返回 `ErrBackpressure`，缓存不越界。
- `TestMissingPredecessorNeverDelivered` — A→C 的 a1 永久丢失后，C 永不交付
  依赖它的消息，时间推进与后续流量都不会"补票"。
- `TestSafetyInvariantUnderFaults` — 20 个种子 × 丢包/重复/乱序下，所有节点
  交付日志均通过 `VerifyLog`（无重复、序号连续、前驱在先）。

活性（`sim_test.go`）：

- `TestLivenessEventuallyAllDelivered` — 无永久丢失 + 背压重传下，20 条广播
  最终全部被 4 个节点交付，缓存清空；背压与去重计数非零（机制确实被触发）。
- `TestConcurrentMessagesStableOrder` — 并发消息在各节点到达顺序不同，但
  `CanonicalOrder`（因果拓扑序 + (Sender,Seq) 确定性 tie-break）在所有节点
  给出相同的稳定排序。

## 运行

```sh
go test ./... -v     # 测试（安全性/活性证据）
go run ./cmd/demo    # 运行样例
```

## 测试结果

```
=== RUN   TestCausalDependencyOrder          --- PASS
=== RUN   TestDeduplication                  --- PASS
=== RUN   TestBackpressureSignal             --- PASS
=== RUN   TestMissingPredecessorNeverDelivered --- PASS
=== RUN   TestConcurrentMessagesStableOrder  --- PASS
=== RUN   TestLivenessEventuallyAllDelivered --- PASS
=== RUN   TestSafetyInvariantUnderFaults     --- PASS
ok  	causalcast
```

## 运行样例输出（节选）

```
== Scenario 1: causal dependency + concurrent messages ==
  node A log: A#1 A#2 B#1 C#1   (buffered=0)
  node B log: A#1 B#1 C#1 A#2   (buffered=0)
  node C log: A#1 C#1 B#1 A#2   (buffered=0)
  canonical stable order: A#1 A#2 B#1 C#1
  safety: all logs valid causal linearizations; canonical order identical at A,B,C

== Scenario 2: missing predecessor is never faked ==
  node C log:   (buffered=3)
  safety: C delivered nothing that depends on the lost A#1 (no timeout can fake it)

== Scenario 3: explicit backpressure + retry ==
  C.Receive(a3) with full buffer -> causal: delivery buffer full
  caller retries a3 after a1 arrives...
  C log after retry: A#1 A#2 A#3  buffered=0
```
