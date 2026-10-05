# bridgeindex

维护无向多重图（允许平行边、禁止自环）的桥集合，以及删除每条桥后
两侧连通块的节点数。边 ID 稳定且不复用，批量更新原子生效。

## 用法

```go
ix := bridgeindex.New()
ids, err := ix.ApplyBatch([]bridgeindex.Update{
    {Kind: bridgeindex.AddEdge, U: 1, V: 2},
    {Kind: bridgeindex.AddEdge, U: 2, V: 3},
})
for _, b := range ix.Bridges() {
    // b.ID, b.U, b.V, b.SideA, b.SideB（SideA <= SideB）
}
```

## 语义

- `ApplyBatch` 先整批校验（自环、未知或重复删除的边 ID），任一非法则
  整批拒绝、索引不变；全部合法才一次性应用并重算桥集合。
- 平行边不会被误判为桥：low-link DFS 按进入边 ID 而非父顶点跳过回边，
  同端点的第二条边会拉低 `low[]`。
- 桥信息在每次成功批量后全量重算，不缓存历史 DFS 序。
- `ReferenceBridges`（reference.go）是核对用的朴素实现：逐边删除后
  重新 BFS 判定连通性，并返回实际受检边数。

## 测试

```sh
go test ./... -v
```

随机小图（含平行边）在每个批量后与参考实现对照，测试日志报告参考
检查器实际受检的边总数。
