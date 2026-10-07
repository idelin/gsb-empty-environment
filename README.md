# paretopath

在有向图（最多 10 个顶点）上枚举源点到终点的全部 Pareto 最优简单路径，
目标为时间与费用双指标（均非负，均为越小越好）。

## 特性

- 不重复顶点：所有路径均为简单路径（通过访问位掩码保证）。
- 非支配标签：标签设置算法（label-setting）按 (time, cost) 字典序出堆，
  每个顶点维护非支配标签集。支配判定要求支配方的已访问顶点集是被支配方的子集，
  否则简单路径约束下剪枝不安全。
- 字典序决胜：指标完全相同的多条路径只保留边序列字典序最小的一条
  （边按 (From, To) 比较）。
- 输出完整 Pareto 前沿，不做任何加权和标量化。

## 用法

```go
g, _ := paretopath.NewGraph(4)
_ = g.AddEdge(0, 1, 1, 9)
_ = g.AddEdge(1, 3, 1, 9)
_ = g.AddEdge(0, 3, 8, 8)
paths, _ := paretopath.ParetoPaths(g, 0, 3)
// paths 按 Time 升序（Cost 降序）给出全部 Pareto 路径
```

## 正确性核对

`BruteForceParetoPaths` 穷举全部简单路径后取 Pareto 前沿；
`TestMatchesBruteForce` 在数百个随机图上逐路径比对两种实现（指标与边序列完全一致）。
