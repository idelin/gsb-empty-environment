# OR-Set（Observed-Remove Set）CRDT + 单进程事件模拟器

纯 Go 标准库实现。`orset` 包是 CRDT 本体，`sim` 包是确定性事件模拟器
（虚拟时间、消息延迟/丢失/重复/乱序、网络分区均由调度器显式控制），
`cmd/demo` 是可运行的演示脚本。

```
orset/orset.go        CRDT：唯一 add 标签、观察删除、墓碑、因果 GC
sim/sim.go            事件模拟器：虚拟时钟 + 优先队列 + 故障注入
cmd/demo/main.go      演示：并发 add/remove、分区、GC、反例
orset/orset_test.go   语义与 GC 安全性测试
sim/sim_test.go       故障下的收敛（活性）与不复活（安全性）测试
```

## 设计

- **唯一 add 标签**：每个事件（add/remove）在副本 `r` 上取 `Dot{r, ++clock}`，
  全局唯一。元素成员 = 所有 add-dot 的集合；`e ∈ set` 当且仅当存在
  未被墓碑覆盖的 add-dot。
- **观察删除**：`Remove(e)` 只把本副本**已观察**的 add-dot 移入墓碑
  （`tombs[e][addDot] = removeDot`）。并发产生、尚未观察到的 add-dot
  不受影响，因此并发 add 与 remove 中 add 保留。
- **合并**：adds 与 tombs 分别做集合并（同一 add-dot 的重复墓碑取
  确定性 max），天然满足交换、结合、幂等 ⇒ 状态型 CRDT 的半格。
- **垃圾回收（因果确认）**：每个副本维护版本向量 `vv` 与矩阵时钟
  `matrix[p][q]`（"据我所知副本 p 已看到 q 的事件直到 …"），随 gossip
  一起传播。remove 事件 `d` 在副本 i 处**因果稳定**当且仅当
  `∀p: matrix_i[p][d.Replica] ≥ d.Seq`，即每个副本都被确认观察到了
  这次删除。只有此时才丢弃墓碑及其杀死的 add-dot。

## 安全性论证（为什么不会复活）

1. **合并安全**：adds/tombs 只增不减（GC 前），合并为并集，故任意
    gossip 顺序、重复、乱序下，读过相同事件集的两个副本状态相同
   （半格性质，测试 `TestMergeCommutativeAssociativeIdempotent`）。
2. **删除语义**：`Remove` 只墓碑化已观察标签，未观察的并发 add 保留
   （`TestRemoveOnlyObservedTags`、`TestConcurrentAddSurvivesRemove`）。
3. **GC 安全（关键不变式）**：`matrix_i[p][q]` 始终是副本 p 真实 `vv[q]`
   的**下界**（只通过 gossip 取 max 单调增长）。因此当 i 判定 remove
   事件 `d` 稳定时，每个副本 p 的真实 `vv[d.Replica] ≥ d.Seq`，即 p
   真的合并过包含该墓碑的状态，p 不再持有被杀的 add-dot 作为活标签。
   归纳：第一个丢弃墓碑的副本丢弃时，全集群已无活的目标 add-dot；
   此后任何合并对端要么仍持有墓碑（合并后元素依然死亡），要么同样
   在确认稳定后才丢弃（其 add-dot 也已清除）。故元素永不复活
   （`TestCausalGCPreventsResurrection`、`TestNoResurrectionInvariant`、
   `TestGCBlockedDuringPartition`）。
4. **反例（为什么必须因果确认）**：若不做因果检查直接丢墓碑
   （`DropTombstonesUnsafe`），未看到 remove 的副本仍持有活 add-dot，
   下次 gossip 会把元素"复活"并传播——
   `TestPrematureTombstoneDropResurrects` 固化该反例。

## 活性论证（最终收敛与 GC 终止）

- **收敛**：假设信道公平（周期性 gossip 下消息最终送达，丢包/重复/
  乱序有界），则每个事件最终到达所有副本；合并单调且状态空间有限，
   gossip 轮次足够后所有副本达到同一不动点 ⇒ 强最终一致性
  （`TestEventualConvergenceUnderFaults`：30% 丢包 + 20% 重复 +
  随机延迟 + 分区修复后收敛）。
- **GC 终止**：remove 事件 `d` 最终到达所有副本（收敛性），各副本
  的 `vv` 最终反映之，矩阵时钟经 gossip 传播后每个副本都能判定 `d`
  稳定，于是墓碑最终被回收（`TestNoResurrectionInvariant` 末尾断言
  所有副本 `TombCount == 0`）。分区期间 GC 安全地阻塞（不做不安全
  回收），分区愈合后自动推进（`TestGCNoOpDuringPartition`）。

## 需求 → 测试对照

| 需求 | 测试 |
|---|---|
| 合并交换/结合/幂等 | `TestMergeCommutativeAssociativeIdempotent` |
| 并发 add 与 remove，未观察 add 保留 | `TestConcurrentAddSurvivesRemove`, `TestRemoveOnlyObservedTags` |
| 重复 remove 无变化 | `TestDuplicateRemoveIsNoOp`, `TestDuplicateDeliveryAndRemove` |
| 过早删墓碑反例 | `TestPrematureTombstoneDropResurrects` |
| GC 需因果确认 | `TestCausalGCPreventsResurrection`, `TestGCBlockedDuringPartition`, `TestGCNoOpDuringPartition` |
| 安全性（不复活） | `TestNoResurrectionInvariant` |
| 活性（故障下收敛 + GC 终止） | `TestEventualConvergenceUnderFaults`, `TestNoResurrectionInvariant` |

## 运行样例

```
$ go run ./cmd/demo
=== OR-Set demo: 3 replicas, simulated network (seed 42) ===
   t=1    A.add("apple")  -> tag A:1
   t=2    B.add("banana")  -> tag B:1
   ...
   t=10   PARTITION: [C] isolated from [A B]
   t=11   A.remove("apple") -> tombstoned 1 observed tag(s), remove event A:2
   t=12   C.add("apple")  -> tag C:1            # 并发、未被 A 观察
   t=14   A -> C : DROPPED (link down)
   ...
   t=20   HEAL: all links restored
   ...
   t=45   A.gc()       -> collected 1 causally-stable tombstone(s)
   t=45   B.gc()       -> collected 1 causally-stable tombstone(s)
   t=45   C.gc()       -> collected 1 causally-stable tombstone(s)
-- final state
   A: elements=[apple banana] tombstones=0 add-dots=2
   B: elements=[apple banana] tombstones=0 add-dots=2
   C: elements=[apple banana] tombstones=0 add-dots=2
   converged=true same-elements=true delivered=86 dropped=4

-- counterexample: premature tombstone drop (no causal check)
   t=4    A.remove("x") -> tombstoned 1 observed tag(s), remove event A:2
   t=5    A drops 1 tombstone(s) WITHOUT causal confirmation
   t=7    B -> A : delivered, A now has [x]     # x 复活！
   => x alive at A after premature GC: true (resurrection anomaly)

same scenario WITH causal GC (tombstone kept until stable):
   t=5    A.gc()       -> collected 0 causally-stable tombstone(s)   # B 未确认，拒绝回收
   ...
   t=18   A.gc()       -> collected 1 causally-stable tombstone(s)
   t=18   B.gc()       -> collected 1 causally-stable tombstone(s)
   => x alive anywhere: A=false B=false; tombstones left: A=0 B=0
```

## 测试结果

```
$ go test ./... -count=1 -v
--- PASS: TestMergeCommutativeAssociativeIdempotent (orset)
--- PASS: TestConcurrentAddSurvivesRemove           (orset)
--- PASS: TestRemoveOnlyObservedTags                (orset)
--- PASS: TestDuplicateRemoveIsNoOp                 (orset)
--- PASS: TestPrematureTombstoneDropResurrects      (orset)  # 反例
--- PASS: TestCausalGCPreventsResurrection          (orset)
--- PASS: TestGCBlockedDuringPartition              (orset)
--- PASS: TestConcurrentRemovesConverge             (orset)
ok      orsetlab/orset
--- PASS: TestEventualConvergenceUnderFaults        (sim)    # 30%丢包+20%重复+分区
--- PASS: TestNoResurrectionInvariant               (sim)    # 安全性+GC活性
--- PASS: TestGCNoOpDuringPartition                 (sim)
--- PASS: TestDuplicateDeliveryAndRemove            (sim)
ok      orsetlab/sim
```

12/12 通过。仅使用标准库（`container/heap`、`math/rand`、`sort`、`fmt`）。
