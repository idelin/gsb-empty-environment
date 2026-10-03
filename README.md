# dagreach — DAG 可达性位图索引器

Go 标准库实现的有向无环图（DAG）全节点可达性查询索引。

## 设计

- **位图传递闭包**：每个节点持有一行 `[]uint64` 位图，第 v 位置位表示 u 可达 v（含自身）。查询 `Reachable(u, v)` 是单次位测试，时间复杂度 **O(1)**。
- **拓扑层级**：节点维护拓扑层级，任意边 `u->v` 满足 `level[u] < level[v]`；插入跨层边时按需对下游子图做增量重标层。
- **增量更新**：插入边 `u->v` 时，把 v 的可达位图按 64 位字并行 `OR` 合并进 u 及其全部祖先；若某祖先合并后无变化（已包含 v 的可达集），该分支立即剪枝。环检测也是一次 O(1) 位图探测（v 已可达 u 则拒绝）。
- **压缩存储**：`Compress()` 生成稀疏字编码的压缩快照——只保存非零的 uint64 字，查询仍为 O(1)（哈希查字 + 位测试），内存占用由非零字数量严格约束。

## 运行

```sh
go test ./...        # 运行测试（含与 BFS 参考实现的全节点对对拍）
go test -bench=. .   # 查询基准
go run ./cmd/demo    # 运行样例
```

## API 摘要

- `NewIndexer(capacity)` / `AddNode() int`
- `AddEdge(u, v) error` — 增量更新传递闭包；返回 `ErrCycle` / `ErrSelfLoop` / `ErrNodeRange`
- `Reachable(u, v) bool` — O(1) 查询
- `Level(u) int` — 拓扑层级
- `Compress() *CompressedIndex` — 压缩快照，`CompressedIndex.Reachable` 同样 O(1)
- `MemoryBytes()` — 内存占用估计（稠密 / 压缩）
