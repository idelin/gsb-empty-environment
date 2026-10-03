# kcore — 图拓扑 K-核分解与剥离器

纯 Go 标准库实现的大型稀疏图核度（Core-Number）分解组件：

- **批量分解**：Batagelj–Zaversnik 桶排序剥离算法，O(V+E) 一次算出所有节点的 Core-Number。
- **分层输出**：`Layers()` 按核度分层，`KCoreNodes(k)` 输出 k-core 子图顶点集（核度 ≥ k）。
- **增量维护**：增删边时依据局部性引理（Sariyuce et al., PVLDB'13）——边 (u,v) 变化只可能影响
  核度等于 `K = min(core(u), core(v))` 的节点、且变化量至多为 1——因此只在受影响的
  局部 K-核连通块内重新传播核度调整（插入时局部晋升剥离、删除时局部降级级联），
  开销远小于全局重算。

## 文件结构

- `kcore.go` — 图结构、批量分解、增量插入/删除传播、分层输出
- `kcore_test.go` — 全部测试（含增量 vs 批量一致性模糊测试）
- `cmd/kcore-demo/main.go` — 验收场景演示

## API 速览

```go
g := kcore.New()
g.AddEdge(1, 2)          // 自动维护核度（已分解时局部传播）
g.RemoveEdge(1, 2)       // 同上，局部降级
g.Decompose()            // O(V+E) 全局重算
g.CoreNumber(v)          // 节点核度
g.KCoreNodes(3)          // 3-core 顶点集
g.Layers()               // 核度 -> 顶点分层
g.LastUpdateWork()       // 上次维护访问的顶点数（更新开销度量）
```

## 运行样例

```
$ go run ./cmd/kcore-demo
Graph: 16 vertices, 22 edges

== Initial decomposition ==
Core-Number per vertex (layered):
  coreness 4: [1 2 3 4 5]
  coreness 2: [10 11 12 13 20 21 22 23]
  coreness 1: [30 31 32]
Layered k-core subgraphs (vertex sets):
  1-core: [1 2 3 4 5 10 11 12 13 20 21 22 23 30 31 32]
  2-core: [1 2 3 4 5 10 11 12 13 20 21 22 23]
  3-core: [1 2 3 4 5]
  4-core: [1 2 3 4 5]

== Insert bridging edge (12,13): completes cluster B into K4 ==
coreness of cluster B before: [2 2 2 2]
coreness of cluster B after:  [3 3 3 3]
incremental update visited 4 vertices
global recomputation visited 16 vertices (4.0x more work)

== Layers after the bridge ==
Core-Number per vertex (layered):
  coreness 4: [1 2 3 4 5]
  coreness 3: [10 11 12 13]
  coreness 2: [20 21 22 23]
  coreness 1: [30 31 32]
Layered k-core subgraphs (vertex sets):
  1-core: [1 2 3 4 5 10 11 12 13 20 21 22 23 30 31 32]
  2-core: [1 2 3 4 5 10 11 12 13 20 21 22 23]
  3-core: [1 2 3 4 5 10 11 12 13]
  4-core: [1 2 3 4 5]

== Remove the bridge again: local demotion ==
coreness of cluster B: [2 2 2 2], visited 4 vertices
```

场景：5-团密集子网（核度 4）+ K4 缺一边的子簇（核度 2）+ 环（核度 2）+ 悬挂链（核度 1）。
加入桥接边 (12,13) 后子簇补全为 K4，4 个节点核度从 2 提升到 3，增量更新仅访问 4 个顶点。

## 测试结果

```
$ go test -v ./...
=== RUN   TestBatchDecomposition
--- PASS: TestBatchDecomposition (0.00s)
=== RUN   TestLayeredSubgraphs
--- PASS: TestLayeredSubgraphs (0.00s)
=== RUN   TestBridgeEdgePromotionIsLocal
    kcore_test.go:105: bridge edge: local update visited 4 vertices vs 16 for global recompute
--- PASS: TestBridgeEdgePromotionIsLocal (0.00s)
=== RUN   TestIncrementalMatchesBatch
--- PASS: TestIncrementalMatchesBatch (1.17s)
=== RUN   TestLargeSparseGraphLocalUpdate
    kcore_test.go:200: n=30004: local update visited 4 vertices, global recompute visited 30004 (7501x)
--- PASS: TestLargeSparseGraphLocalUpdate (2.69s)
=== RUN   TestEdgeCases
--- PASS: TestEdgeCases (0.00s)
PASS
ok  	kcore	4.296s
```

- `TestBatchDecomposition` / `TestLayeredSubgraphs`：验收图的分层 1-core..4-core 结果正确。
- `TestBridgeEdgePromotionIsLocal`：桥接边使局部核度 2→3，更新只触及 4 个顶点。
- `TestIncrementalMatchesBatch`：600 次随机增删边后，增量维护结果与全局重算逐一相等。
- `TestLargeSparseGraphLocalUpdate`：3 万顶点稀疏图上局部更新访问 4 个顶点 vs 全局 30004 个（约 7500 倍差距）。
