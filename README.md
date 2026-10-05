# sparse2d

稀疏点集的在线二维范围查询库（Go）。点以稳定 string ID 增删，查询半开矩形
`[x0,x1) × [y0,y1)` 内的点数与整数权重和。

## 特性

- **在线坐标**：坐标以任意顺序在线出现，无需预压缩表；坐标与权重均为 `int64`，
  支持 `math.MinInt64` 等量级的极大稀疏坐标。
- **重复坐标分别计费**：内层索引以 `(y, id)` 为键，同一坐标下不同 ID 各自计数。
- **原子替换**：`Replace` 在写锁内完成 删除+插入，并发查询不会观察到中间状态。
- **索引访问统计**：`Stats()` 输出查询数、增/删/替换次数与查询访问的节点数
  （`NodeVisits`），`ResetStats()` 清零。
- **整数溢出处理**：每个子树把精确和表示为 `sum + carry·2^64`（`sum` 为回绕后的
  int64，`carry` 为 2^64 的倍数计数）。因此 `Result.Sum` 恒等于数学和对 2^64 取模，
  `Result.Overflow` 精确报告数学和是否超出 int64 范围——即使中间部分和回绕而
  总和仍在范围内，也不会误报。

## 结构

外层 treap 按 `x` 建索引（每个不同的 x 一个节点），每个外层节点挂一棵按
`(y, id)` 建索引的内层 treap，内层节点维护子树聚合（count、sum、carry、
minY/maxY）。treap 优先级由键的 splitmix64 哈希确定，行为可复现。
查询时外层按 x 剪枝，内层利用 minY/maxY 对整棵子树直接采纳聚合值或整体跳过。

## API

```go
ix := sparse2d.New()
ok := ix.Add("id", x, y, weight)      // id 已存在时返回 false，不做修改
ok = ix.Remove("id")                  // id 不存在时返回 false
ok = ix.Replace("id", x, y, weight)   // 原子替换；id 不存在时返回 false
p, ok := ix.Get("id")
r := ix.Query(x0, y0, x1, y1)         // 半开矩形；x1<=x0 或 y1<=y0 时为零结果
n := ix.Len(); nx := ix.DistinctX()
st := ix.Stats(); ix.ResetStats()
```

注意：上界为排他的 int64，因此坐标恰为 `math.MaxInt64` 的点无法被任何矩形覆盖
（其后继不可表示）；其余所有坐标（含 `math.MinInt64`）均正常。

## 验证

`index_test.go` 用全扫描（big.Int 精确求和）对拍：半开边界网格、负权重、
极大稀疏坐标、重复坐标、原子替换、溢出（正/负/部分和回绕），以及 4000 步
随机操作序列 + 随机矩形查询。演示程序见 `cmd/demo`。

```sh
go test ./...
go run ./cmd/demo
```
