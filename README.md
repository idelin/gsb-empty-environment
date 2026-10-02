# dyntopo — 双向增量拓扑排序与环检测器

面向大型依赖图的动态拓扑序维护引擎（Go，仅标准库）。核心算法为
Pearce–Kelly 动态拓扑排序：每个节点持有一个稠密整数"染色"（拓扑序号），
插入边时通过**双向局部搜索 + 局部重染色**把受影响节点数降到最低。

## 特性

- **并发安全**：所有变更经 `sync.RWMutex` 串行化，支持并发 `AddEdge` 与并发读。
- **立即拒绝成环边**：插入 `x->y` 时若前向搜索触及 `x`，即判定成环，
  返回 `*CycleError`，`Path` 为完整因果死锁环路（首尾闭合，如 `[2 3 4 2]`），
  且不修改任何已有拓扑序号与边。
- **快速路径**：`ord[x] < ord[y]` 时 O(1) 直接插边，无需重排。
- **双向局部重染色**：否则从 `y` 前向 DFS（上界 `ord[x]`）得 `deltaF`、
  从 `x` 反向 DFS（下界 `ord[y]`）得 `deltaB`，仅交换这两个区块已有的序号值，
  块内相对顺序不变，区域外节点完全不受影响。

## 文件

- `topo.go` — 引擎实现（`Graph`、`AddEdge`、`CycleError`、`TopologicalOrder`、`Validate`）
- `topo_test.go` — 验收与并发测试
- `cmd/demo/main.go` — 运行样例

## 运行样例

```
$ go run ./cmd/demo
== build initial dependencies 1->2->3->4 ==
topological order: [1 2 3 4]

== insert 4->2 (closes a deadlock cycle) ==
rejected: topo: edge 4->2 rejected, dependency cycle: [2 3 4 2]
order after rejection: [1 2 3 4]

== insert 1->4 (fast path, no recoloring) ==
topological order: [1 2 3 4]

== insert 5->2 (bidirectional local recoloring) ==
before: [1 2 3 4 5]
after:  [1 5 2 3 4]
valid:  true
```

## 测试结果

```
$ go test -race -v ./...
=== RUN   TestCycleRejectedWithExactPath      # 4->2 被拒，精准返回 [2 3 4 2]，原序不变
--- PASS: TestCycleRejectedWithExactPath (0.00s)
=== RUN   TestFastPathKeepsMonotonicOrder     # 1->4 快速路径，序号保持单调
--- PASS: TestFastPathKeepsMonotonicOrder (0.00s)
=== RUN   TestBidirectionalLocalRecolor       # 5->2 局部重染色，未受影响节点不动
--- PASS: TestBidirectionalLocalRecolor (0.00s)
=== RUN   TestSelfLoopRejected
--- PASS: TestSelfLoopRejected (0.00s)
=== RUN   TestDuplicateEdgeIsNoOp
--- PASS: TestDuplicateEdgeIsNoOp (0.00s)
=== RUN   TestConcurrentAdds                  # 8 goroutine 并发插入 2000 节点 DAG
--- PASS: TestConcurrentAdds (0.01s)
=== RUN   TestConcurrentCycleDetection        # 16 goroutine 竞争插入成环边，全部被拒
--- PASS: TestConcurrentCycleDetection (0.00s)
PASS
ok      dyntopo 1.877s
```
