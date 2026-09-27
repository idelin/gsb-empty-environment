# clocksync

根据请求/响应的发送-接收因果事件，推导各节点时钟偏移（clock offset）的
可行区间；识别互相矛盾的约束并返回冲突链。仅依赖 Go 标准库。

遥测事件在本地合成，通过 JSON 夹具回放；不接监控平台，也不做纯展示看板。

## 模型

设节点 `v` 本地时钟相对参考节点的偏移为 `theta[v]`（本地时间 = 真实时间 +
`theta[v]`）。对一条消息 `u -> v`（u 在本地 `send` 发送，v 在本地 `recv`
接收，网络时延 `d >= 0`）：

```
recv - send = theta[v] - theta[u] + d,  d >= 0
```

因此每条消息给出一条差分约束：

```
theta[v] - theta[u] <= recv - send
```

- 界 `recv - send` 即使为负也原样保留，绝不截断成 0；负界正是暴露矛盾
  的关键。
- 一组往返约束形成两条方向相反的边，给出偏移的上下界。
- 求解用 Bellman-Ford 差分约束系统：
  - 无负环：`dist(ref, v)` 与 `dist(v, ref)` 给出 `theta[v]` 的最紧上下界
    （即可行区间），缺约束的方向为 `-inf/+inf` 宽区间。
  - 有负环：约束矛盾，沿前驱回溯提取负环作为冲突链，环上界之和 `< 0`。
- 校正取 Bellman-Ford 的可行势（平移到参考节点为 0）。因为每条消息的
  约束都被满足，校正后 `recv' - send' >= 0`，因果边不会倒序；不做任何
  “把负耗时截成 0”的掩盖处理。

## 布局

```
interval.go                 区间与交
telemetry.go                事件、夹具加载、send/recv 配对
constraints.go              消息 -> 差分约束（保留负界）
solver.go                   Bellman-Ford、区间、负环冲突链
correct.go                  时间校正与因果边校验
cmd/clocksync/main.go       夹具回放 CLI
fixtures/*.json             consistent / conflict / sparse 三组夹具
*_test.go                   go test 用例（含夹具回放）
```

## 运行样例

```text
$ go run ./cmd/clocksync fixtures/consistent.json
fixture: fixtures/consistent.json
replayed 12 events -> 6 messages, 6 constraints, 3 nodes
reference node: A (offset fixed to 0)
node A: offset interval [-0.000, +0.000]  picked +0.000
node B: offset interval [+45.000, +57.000]  picked +45.000
node C: offset interval [-33.000, -21.000]  picked -31.000
causal check: 6/6 edges non-reversed after correction

$ go run ./cmd/clocksync fixtures/conflict.json
fixture: fixtures/conflict.json
replayed 4 events -> 2 messages, 2 constraints, 2 nodes
conflicting constraints (cycle total -5.000 < 0):
  A --m1--> B: theta[B] - theta[A] <= +5.000
  B --m2--> A: theta[A] - theta[B] <= -10.000
(exit code 1)

$ go run ./cmd/clocksync fixtures/sparse.json
fixture: fixtures/sparse.json
replayed 2 events -> 1 messages, 1 constraints, 3 nodes
reference node: A (offset fixed to 0)
node A: offset interval [-0.000, +0.000]  picked +0.000
node B: offset interval [-inf, +40.000]  picked +0.000
node C: offset interval [-inf, +inf]  picked +0.000
causal check: 1/1 edges non-reversed after correction
```

- `consistent.json` 按真实偏移 `theta[B]=+50, theta[C]=-30` 合成，二者均
  落在求出的区间内。
- `conflict.json` 的往返约束等价于偏移既要 `<= 5` 又要 `>= 10`，返回完整
  冲突链（总界 -5），而不是把负时延截断为 0。
- `sparse.json` 只有单向约束：B 只有上界，C 无约束，返回宽区间。

## 测试结果

```text
$ go test ./... -v
=== RUN   TestCorrectedCausalEdgesNotReversed
--- PASS: TestCorrectedCausalEdgesNotReversed (0.00s)
=== RUN   TestCheckCausalDetectsReversal
--- PASS: TestCheckCausalDetectsReversal (0.00s)
=== RUN   TestPairMessagesErrors
--- PASS: TestPairMessagesErrors (0.00s)
=== RUN   TestReplayConsistentFixture
--- PASS: TestReplayConsistentFixture (0.00s)
=== RUN   TestReplayConflictFixture
--- PASS: TestReplayConflictFixture (0.00s)
=== RUN   TestReplaySparseFixture
--- PASS: TestReplaySparseFixture (0.00s)
=== RUN   TestIntervalIntersect
--- PASS: TestIntervalIntersect (0.00s)
=== RUN   TestDeriveConstraintsKeepsNegativeBound
--- PASS: TestDeriveConstraintsKeepsNegativeBound (0.00s)
=== RUN   TestSolveConsistentRoundTrip
--- PASS: TestSolveConsistentRoundTrip (0.00s)
=== RUN   TestSolveConflictReturnsChain
--- PASS: TestSolveConflictReturnsChain (0.00s)
=== RUN   TestSolveThreeNodeConflictChain
--- PASS: TestSolveThreeNodeConflictChain (0.00s)
=== RUN   TestSolveSparseReturnsWideIntervals
--- PASS: TestSolveSparseReturnsWideIntervals (0.00s)
=== RUN   TestSolveTransitiveTightening
--- PASS: TestSolveTransitiveTightening (0.00s)
PASS
ok  	clocksync	0.431s
```
