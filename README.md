# roadnet

Go 求解库：非负整数权有向多重图上的替换路径（replacement paths）与
带有效时间区间 / 修订号的路网快照查询及路径缓存。

## graph — 替换路径

- 图为有向多重图，平行边按唯一边 ID 区分；权值为非负整数（`int64`）。
- `Graph.ShortestPath(s, t, banned)`：Dijkstra；`banned` 只删除该 ID 的
  那一条边，平行边仍可用；`graph.NoEdge` 表示不删边。
- `Graph.ReplacementPaths(s, t, path)`：对给定最短路径上的每条边 e，
  独立跑一次 Dijkstra，输出 `G - {e}` 中的最短距离与一条证据路径
  （`Witness`）；不可达时 `Reachable=false` 显式标记。
- 零权边 / 零权环、多条等长最短路均被支持（等长时返回确定性的一條）。

测试用**逐边暴力枚举所有简单路径**（非负权下充分）与求解器交叉核对，
覆盖平行边、零权环、等长最短路、不可达与随机图。

## versioned — 时间区间 + 修订号快照与缓存

- 边有基础成本（revision 0，全时有效）；`CostChange` / `ClosureEvent`
  带有效区间 `[From, To)`，每次 `Revise` 作为新修订号追加（append-only，
  旧版本快照不可变）。拓扑在首次 `Revise` 后固定。
- 查询固定**知晓版本**与**出发时间**：`FrozenGraph(knowRev, depart)`
  生成一张冻结静态图，路径上所有边按同一冻结图判断（取修订号 ≤
  knowRev 且区间覆盖 depart 的最新记录），不做到达时间传播。
- `Cache` 按 `Query{S, T, Depart}` 缓存当前版本答案。局部修订后按
  依赖失效：
  - 路径上的边发生任何变化（涨价、降价、封闭）→ 失效；
  - 路径外的边**变得更便宜**（降价或封闭解除）→ 失效；
  - 路径外的边变贵或封闭 → 保留（备选路径只会更差，最短性证书仍成立）。
- 缓存的最短性**不能只按路径上的边失效**：外部边降价会产生更短路径。
  `TestOffPathPriceDropCounterexample` 构造该反例——只查路径边的朴素
  策略会返回过期答案 6，而全量重算为 4；本缓存正确失效并与
  `ShortestPathAt` 全量重算一致。随机化测试在每次修订后以全量重算
  逐条核对缓存答案。

## 运行

```sh
go test ./...
```
