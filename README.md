分层导航小世界（HNSW）向量索引在批量删除高连通枢纽节点后，简单打上墓碑标记会让查询陷入孤岛或退化为全扫描，必须在受限度上限内对其邻域执行多样性剪枝重建双向连接。为向量检索存储交付可导入的 Go 图维护包。输入多层无向图（节点坐标与层高、每层最大度数 `M` 及底层 `M0=2M`、调用方注入的距离函数）、待删除节点集合与搜索参数 `ef`。规则：执行 HNSW 论文 Algorithm 4 的启发式选边（对候选集按距目标升序遍历，仅当候选点到目标的距离严格小于它到所有已选邻居的距离时才选入，可选 `keepPrunedConnections` 用被剪掉的最近候选补满度上限）；删除节点后，将其从所有存活邻居的每层邻接表移除，并对每个受影响存活节点，以原删除节点及其存活一跳邻居为候选池重新按启发式补边并对称更新反向边（反向边超度上限时同样执行启发式缩边），同时维护合法最高层入口点（原入口被删时降级为剩余最高层节点）；查询接口跳过软删节点但确保连通性与排序确定性。验收对照精确暴力近邻与图不变量（无指向已删节点的悬空边、双向边严格对称、每层度数不超过上限、入口点必存活），覆盖删除顶层入口节点引发入口迁移、删除桥接枢纽后邻域补边恢复连通、启发式多样性优先保留另一簇远点而非聚集近点，以及空图与全删边界。

## `hnsw` 包

导入路径为 `prescreen.local/ps-eb477846fe/hnsw`，基于泛型实现，向量类型由调用方决定，距离函数通过 `Config.Distance` 注入。

- `graph.go`：图、节点、邻接表与导入 API（`NewGraph`、`AddNode`、`AddEdge`、`SetEntry`）。
- `heuristic.go`：HNSW 论文 Algorithm 4 启发式选边、对称加/删边与超度缩边不动点。
- `delete.go`：批量软删、邻域候选池重建、反向边缩边与入口点迁移。
- `connectivity.go`：layer-0 连通分量检查与删除后的重连兜底。
- `search.go`：上层贪心下降、layer-0 `ef` 束搜索，软删节点跳过，`(距离, ID)` 确定性排序。
- `invariants.go`：悬空边、双向对称、度上限与入口点合法性的聚合校验。

### 快速使用

```go
g, _ := hnsw.NewGraph[point](hnsw.Config[point]{
    M:                     4,          // layer 0 上限 M0 = 2M = 8
    Distance:              euclidean,  // 调用方注入
    KeepPrunedConnections: true,
})
_ = g.AddNode(0, point{0, 0}, 2)
_ = g.AddEdge(1, 0, 1)

report, err := g.Delete([]int{0})     // 批量软删并重建双向邻接
if err := g.CheckInvariants(); err != nil { ... }

hits, err := g.KNN(query, k, ef)      // 跳过墓碑，确定性排序
```

### 删除语义

1. 快照所有相关层的邻接表，再从存活邻居上对称摘除待删节点。
2. 对每个受影响存活节点，以“原删除邻居 + 其一跳邻居 + 当前存活邻居及其二跳”为候选池，按 Algorithm 4 重选边并对称写回。
3. 加边导致任一端点超过 `M`（上层）或 `M0=2M`（layer 0）时，对该端点执行同一启发式缩边，并递归保持反向对称。
4. 若整批删除使某节点本地候选池为空，则连接到全局最近存活点；最后对 layer-0 做连通分量兜底，杜绝孤岛。
5. 入口被删时迁移到剩余最高层节点，同层取最小 ID；全部删光后入口为空。

### 验收测试

`go test ./...` 覆盖：

- `TestHeuristicDiversity`：启发式优先保留另一簇远点，`keepPrunedConnections` 补满度上限。
- `TestDeleteBridgeHub`：删除桥接枢纽后邻域补边恢复连通，并对齐暴力近邻。
- `TestDeleteEntryMigration`：删除顶层入口触发逐级入口迁移，含全删与幂等重删。
- `TestReverseShrinkHeuristic`：反向边超度时启发式缩边且严格保持对称与度上限。
- `TestKNNMatchesBruteForce` / `TestRandomizedDeletionStress`：多轮随机删除下逐次校验不变量、连通性与精确暴力近邻排名。
- `TestEmptyAndFullBoundaries`：空图、全删、未知 ID 与软删查询等边界。
