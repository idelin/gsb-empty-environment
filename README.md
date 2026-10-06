# paretopath

在顶点数不超过 10 的有向图上，求时间-费用双目标的全部 Pareto 最优简单路径。

## 特性

- 双指标（时间、费用）均非负，标签校正算法枚举非支配标签
- 路径不重复顶点（简单路径）
- 指标完全相同的标签只保留字典序最小的边序列
- 输出完整 Pareto 前沿，而非单一加权和最优解
- `SimplePathLabels` + `ParetoFilter` 提供穷举枚举，用于交叉核对

## 用法

```go
g := paretopath.NewGraph(4)
g.AddEdge(0, 1, 1, 10)
g.AddEdge(1, 3, 0, 0)
g.AddEdge(0, 3, 5, 7)
g.AddEdge(0, 2, 10, 1)
g.AddEdge(2, 3, 0, 0)

labels := g.ParetoPaths(0, 3)
// [{1 10 [0 1 3]} {5 7 [0 3]} {10 1 [0 2 3]}]
```

## 测试

```sh
GO111MODULE=on go test ./...
```
