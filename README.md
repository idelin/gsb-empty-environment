# incmaxflow — 增量最大流与最小割证书生成器

有向网络整数容量最大流的动态调整器（Go，仅标准库）。

## 功能

- **初始最大流**：Edmonds-Karp（BFS 最短增广轨），`MaxFlow(s, t)`。
- **批量容量调整**：`BatchUpdate` / `UpdateCapacity` 支持对已有边增容或减容。
- **反向流撤回**：减容导致边上当前流量超限时，先把该边流量钳制到新容量，
  再沿残余网络把多余流量从边上端点撤回源点 `s`（并从汇点 `t` 拉回），恢复流量守恒。
- **替代路径重增广**：撤回后在残余网络上继续增广，找回可经其他路径输送的流量。
- **最小割证书**：每次变更输出新最大流量值、S 侧可达性划分（`Reachable`）
  以及跨越 S→T 的割边集合（`Cut`），割容量之和等于最大流值。

## 验收场景网络

```
s(0) --5--> a(1) --5--> t(3)
  \          |           ^
   5         1           |
    \        v           6
     \-----> b(2) -------/
```

初始最大流 10；关键边 `a->t` 从 5 降至 2 时，沿反向增广轨撤回 3 单位流量，
再经替代路径 `s->a->b->t` 找回 1 单位，最终流量 8，
割集为 `{s->b(5), a->t(2), a->b(1)}`，节点划分为 `{s,a} | {b,t}`。

## 使用

```go
net := maxflow.NewNetwork(4)
eAT := net.AddEdge(1, 3, 5) // ... 添加其余边
net.MaxFlow(0, 3)           // 初始最大流 = 10
res := net.UpdateCapacity(eAT, 2)
// res.MaxFlow = 8, res.Retracted = 3, res.ReAugmented = 1
// res.Reachable = [true true false false], res.Cut = 割边集合
```

批量变更：`net.BatchUpdate([]maxflow.CapUpdate{{ID: e1, NewCap: 2}, ...})`。

## 运行样例

```
$ go run ./cmd/demo
=== Initial network ===
max flow = 10
  flow(s->a) = 5
  flow(s->b) = 5
  flow(a->t) = 5
  flow(a->b) = 0
  flow(b->t) = 5

=== Update: capacity of a->t reduced 5 -> 2 ===
retracted along residual network : 3 units
re-augmented via alternative path: 1 units
new max flow                     : 8
  flow(s->a) = 3
  flow(s->b) = 5
  flow(a->t) = 2
  flow(a->b) = 1
  flow(b->t) = 6

=== Min-cut certificate ===
S side (reachable from s): {s, a}
T side                     : {b, t}
cut edge: s->b  cap=5
cut edge: a->t  cap=2
cut edge: a->b  cap=1
cut capacity = 8 (== max flow, certificate valid)
```

## 测试结果

```
$ go test ./... -v
=== RUN   TestInitialMaxFlow
--- PASS: TestInitialMaxFlow (0.00s)
=== RUN   TestCapacityReductionWithRetraction
--- PASS: TestCapacityReductionWithRetraction (0.00s)
=== RUN   TestCapacityIncrease
--- PASS: TestCapacityIncrease (0.00s)
=== RUN   TestBatchUpdate
--- PASS: TestBatchUpdate (0.00s)
=== RUN   TestReductionWithoutViolationKeepsFlow
--- PASS: TestReductionWithoutViolationKeepsFlow (0.00s)
=== RUN   TestCertificateMatchesBruteForce
--- PASS: TestCertificateMatchesBruteForce (0.00s)
PASS
ok  	example.com/incmaxflow
```

覆盖：初始最大流、减容触发撤回与重增广（验收边界）、增容恢复、
批量混合变更、未超限减容不改变流量、以及连续变更序列下
"割容量 == 最大流值"证书的逐步校验。
