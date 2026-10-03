# itc — Interval Tree Clock（区间时间戳 / 无中心逻辑时钟）

Go 实现的 **Interval Tree Clock (ITC)**：面向动态系统的无中心逻辑时钟。
仅使用标准库，无任何第三方依赖。

参考论文：Paulo Sergio Almeida, Carlos Baquero, Victor Fonte,
*Interval Tree Clocks: A Logical Clock for Dynamic Systems*, SRDS 2008.

## 特性

- **零配置、无中心**：无需预先设定集群节点列表，无协调者。
- **动态拓扑**：任意节点可随时加入（由任一现存节点 `Fork` 出一个子时钟）
  或退出（直接丢弃时钟即可，区间随 `Join` 归还）。
- **精确因果跟踪**：通过递归划分时间区间 `[0, 1]` 记录分叉/合并，
  事件树（event tree）完整保留因果历史，可判定先后与并发关系。
- **不可变设计**：所有操作均为纯函数式，返回新 `Stamp`，不修改接收者。

## 核心概念

每个时钟是一个 `Stamp = (ID, Event)` 对：

- **ID**：二叉树，描述节点拥有的 `[0, 1]` 区间切片。叶子 `1` 表示拥有全部，
  叶子 `0` 表示没有，内部节点 `(l, r)` 表示左右各半。
- **Event**：计数器树，记录因果历史。`(n, l, r)` 中 `n` 为基准计数，
  `l`、`r` 为相对基准的偏移，路径上的绝对计数为沿途基准之和。

## API

```go
root := itc.Seed()        // 初始单节点：持有完整区间 {1; 0}
root = root.Event()       // 记录本地事件（ITC 的 event 操作）

a, b := root.Fork()       // 裂变为两个各持一半的子时钟（新节点加入）
a = a.Event()             // 两侧并发演进
b = b.Event().Event()

a.Concurrent(b)           // true：二者无因果先后
j := itc.Join(a, b)       // 合并区间，精确保留双方因果历史
a.Leq(j)                  // true：join 的结果支配两个输入
j.ID()                    // 1：完整区间恢复
```

## 运行样例

```console
$ go run ./cmd/demo
=== Interval Tree Clock demo ===

-- 1. One node owns the whole interval [0, 1]
   seed:        {1; 0}
   root ticks:  {1; 1}

-- 2. Fork: split [0, 1] into two halves, no coordination needed
   a:           {(1, 0); 1}
   b:           {(0, 1); 1}

-- 3. Both sides evolve independently (concurrently)
   a ticks 1x:  {(1, 0); (1, 1, 0)}
   b ticks 2x:  {(0, 1); (1, 0, 2)}
   relation:    CONCURRENT

-- 4. Join: merge both halves, preserving causal history
   join(a, b):  {1; (1, 1, 2)}  (full interval restored)
   a <= join? true, b <= join? true
   join ticks:  {1; 3}  -> now dominates all prior history: true, true

-- 5. Dynamic topology: a new node c joins by forking a
   a forks:     {((1, 0), 0); (1, 1, 0)}
   c (new):     {((0, 1), 0); (1, 1, 0)}
   a2 ticks:    {((1, 0), 0); (1, (1, 1, 0), 0)}
   c ticks:     {((0, 1), 0); (1, (1, 0, 1), 0)}
   a2 vs c:     CONCURRENT

-- 6. c leaves silently, then rejoins later: causality intact
   join(a2, c): {(1, 0); (1, 2, 0)}
   a2 <= m? true, c <= m? true
   m vs b:      CONCURRENT (b never heard from a2 or c)
   join all:    {1; 3}
   every node <= full? a2:true b:true c:true
```

## 测试

```console
$ go test -v ./...
=== RUN   TestSeedHoldsFullInterval
--- PASS: TestSeedHoldsFullInterval (0.00s)
=== RUN   TestForkSplitsInterval
--- PASS: TestForkSplitsInterval (0.00s)
=== RUN   TestForkConcurrentJoin
--- PASS: TestForkConcurrentJoin (0.00s)
=== RUN   TestJoinPreservesBothHistories
--- PASS: TestJoinPreservesBothHistories (0.00s)
=== RUN   TestDynamicJoinAndLeave
--- PASS: TestDynamicJoinAndLeave (0.00s)
=== RUN   TestEventMonotonic
--- PASS: TestEventMonotonic (0.00s)
=== RUN   TestRecursiveFork
--- PASS: TestRecursiveFork (0.00s)
=== RUN   TestJoinOverlappingPanics
--- PASS: TestJoinOverlappingPanics (0.00s)
=== RUN   TestImmutability
--- PASS: TestImmutability (0.00s)
=== RUN   TestRandomizedConsistency
--- PASS: TestRandomizedConsistency (0.22s)
PASS
ok  	itc	0.386s
```

测试覆盖验收边界：

- `TestSeedHoldsFullInterval`：初始单节点持有完整区间 `[0, 1]`。
- `TestForkConcurrentJoin`：Fork 裂变为两个各持一半的子时钟，异地并发更新后
  Join 合并，精确保留双方因果先后（join 严格支配两个并发输入）。
- `TestDynamicJoinAndLeave`：第三节点运行时 Fork 加入、静默退出、稍后重新
  Join，因果历史不丢失；全量合并恢复完整区间。
- `TestRandomizedConsistency`：2000 步随机 Fork/Event/Join 操作，校验
  因果序与 join 定义自洽（`a <= b` 当且仅当 `join(a, b) == b`）、
  Join 恒支配输入、身份区间合并恒还原为 `1`。

## 项目结构

```
itc.go           核心库：ID/Event 树与 Seed/Fork/Event/Join/Leq 操作
itc_test.go      go test 测试
cmd/demo/main.go 可运行样例
```
