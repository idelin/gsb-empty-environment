# dyntopo — 双向增量拓扑排序与环检测器

大型依赖图的动态拓扑序维护引擎（Pearce-Kelly 算法），仅使用 Go 标准库，并发安全。

## 特性

- **并发加边**：`AddEdge` 内部互斥串行化，多 goroutine 可安全并发调用。
- **成环立即拒绝**：返回完整因果死锁环路（首尾相接，如 `[2 3 4 2]`），原拓扑序零改动。
- **双向局部重染色**：未成环时仅对受影响区间 `[order(to), order(from)]` 内、
  从 `to` 前向可达 / 可达 `from` 的节点重新标号，区间外节点序号完全不变。

## 运行

```sh
go test -race ./...   # 测试
go run ./cmd/demo     # 样例
```

## API

```go
g := dyntopo.New[int]()
g.AddEdge(1, 2)              // (nil, false) 接受
cyc, rejected := g.AddEdge(4, 2) // 成环时 rejected=true, cyc=[2 3 4 2]
g.TopoOrder()                // 当前拓扑序快照
g.Order(v) / g.HasEdge(u, v) / g.Edges() / g.Size()
```
