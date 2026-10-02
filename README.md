# fas — 有向图反馈弧集（FAS）极小近似器

破环切边优化器：在加权有向图中寻找总权重近似最小的边集，移除后使图成为
有向无环图（DAG），并输出剩余图的拓扑序列作为**严格无环的构造性证明**。
仅使用 Go 标准库。

## 算法

`Solve` 运行三种启发式并取总权重最轻的结果（平局取边数更少者）：

| 启发式 | 策略 |
| --- | --- |
| `GreedyShared` | 每轮定位被最多现存有向环**共享**的关键边切除，平局取权重最高者 |
| `GreedyMinWeight` | 每轮找出一个有向环，切除其上权重最小的边 |
| `Topological` | 加权 Eades–Lin–Smyth 排序启发式，排序中的反向边构成反馈弧集 |

无环性证明：`TopoSort`（Kahn 算法）对剩余图生成拓扑序列；
`VerifyOrder` 校验该序列是全节点的排列且每条剩余边都从序列前部指向后部 —
— 这等价于严格无环的数学证明。

## 运行样例

```sh
go run ./cmd/fas-demo
```

样例图包含三个互相交叠的有向环 X: 0→1→2→0、Y: 0→1→3→0、
Z: 0→1→2→4→3→0，它们共享关键边 0→1（权重 5，其余边均为 6）。
输出：

```
Directed graph with overlapping cycles X, Y, Z:
  0 -> 1  (w=5)
  1 -> 2  (w=6)
  2 -> 0  (w=6)
  1 -> 3  (w=6)
  3 -> 0  (w=6)
  2 -> 4  (w=6)
  4 -> 3  (w=6)
input graph is a DAG: false

Heuristic results:
  greedy-shared:     cut [0->1(w=5)]  total weight 5
  greedy-min-weight: cut [0->1(w=5)]  total weight 5
  topological:       cut [0->1(w=5)]  total weight 5

Hybrid choice: greedy-shared, total removed weight 5
removed edge set: [0->1(w=5)]
remaining graph is a DAG: true
topological order of remaining graph: [1 2 4 3 0]
topological order verified: true
```

算法定位到被三个环共享的关键边 `0->1`，一刀切除即破除全部环
（否则至少需要两条权重 6 的边，总权重 12）。

## 测试

```sh
go test -v ./...
```

测试结果（11 项全部通过）：

```
--- PASS: TestSampleGraphHasOverlappingCycles      # 交叠环夹具：非关键边单切不破全部环
--- PASS: TestGreedySharedCutsKeyEdge              # 共享贪心定位关键边 0->1
--- PASS: TestGreedyMinWeightCutsKeyEdge           # 最小权贪心同样切中关键边
--- PASS: TestHybridPicksLightestResult            # 混合求解取到最优权重 5
--- PASS: TestHybridBeatsIndividualHeuristics      # 单启发式次优（暴力枚举验证）时混合仍最优
--- PASS: TestRemainingGraphIsStrictlyAcyclic      # 剩余图严格无环 + 拓扑序列逐边校验
--- PASS: TestDAGInputNeedsNoRemoval               # DAG 输入返回空边集
--- PASS: TestSelfLoopIsRemoved                    # 自环必被三种启发式移除
--- PASS: TestDisconnectedCycles                   # 多个不相交环分别切最便宜的边
--- PASS: TestVerifyOrderRejectsBadOrder           # 非法拓扑序列被拒绝
--- PASS: TestRandomGraphsAlwaysAcyclicRemainder   # 200 组随机图性质测试
PASS
ok  	fas
```

## 复杂度

- `GreedyShared`：每轮对每条边做一次 BFS 找环，O(轮数 · E · (V + E))
- `GreedyMinWeight`：每轮一次 DFS 找环，O(轮数 · (V + E))
- `Topological`：O(V · (V + E))
- 验证：`TopoSort` / `VerifyOrder` 均为 O(V + E)
