# incscc — 增量强连通分量维护器

动态有向图的 SCC 维护组件，纯 Go 标准库实现。

## 特性

- 顶点动态增删：`AddVertex` / `RemoveVertex`（自动清理关联边）
- 有向边插入/删除：`AddEdge` / `RemoveEdge`（自动补全缺失端点）
- 每次变更返回 SCC 差量 `Change`：合并记录 `Merges` 与分裂记录 `Splits`
- 稳定的分量标识：分量 ID 恒为成员中的最小节点 ID

## 实现要点

- 维护正/反邻接表与缩点视图（`comp` / `comps`）
- 插边合并：仅当 `v` 可达 `u`（成环）时，用一次前向遍历与一次反向
  遍历的交集算出被合并的节点集合，合并涉及的全部分量
- 删边/删点分裂：仅对受影响的单个分量在其诱导子图上局部重跑
  Tarjan，得到精准的分裂结果

## 使用

```go
m := scc.New()
m.AddEdge("A", "B")
m.AddEdge("B", "C")
ch := m.AddEdge("C", "A")   // ch.Merges: [A B C] -> 分量 "A"{A,B,C}
m.AddVertex("D")
m.AddEdge("C", "D")         // 无环，无差量
ch = m.RemoveEdge("B", "C") // ch.Splits: "A" -> {A},{B},{C}；D 不受影响
```

## 运行

```sh
go test ./...   # 测试（含验收边界用例与随机对拍）
go run ./cmd/demo
```
