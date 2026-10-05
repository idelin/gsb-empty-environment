# sparse2d — 稀疏点集二维查询库（Go）

按稳定 ID 增删点，查询半开矩形 `[MinX,MaxX) x [MinY,MaxY)` 内的点数与整数权重和。

## 设计

- **在线坐标压缩**：不预设固定压缩表。每个新出现的 x 坐标插入有序数组；
  每个 x 节点内对 y 坐标做同样的在线离散化，并维护两棵 Fenwick 树
  （计数、权重和）。坐标可以以任意顺序、任意稀疏程度出现（含 `math.MinInt64` 附近）。
- **稳定 ID**：`points map[string]Point` 保证 ID 唯一；相同坐标、不同 ID 的点
  分别计数、分别计费（同一 y 槽位计数累加）。
- **原子替换**：`Replace` 在同一把写锁内完成旧点删除 + 新点插入，
  查询（读锁）永远不会看到中间态。整个索引 `sync.RWMutex` 保护，可并发查询。
- **整数溢出处理**：权重和全程用 128 位二进制补码精确累加
  （`math/bits`），结果与插入/枚举顺序无关。精确总和超出 int64 时，
  `Result.Sum` 饱和到 `math.MaxInt64` / `math.MinInt64` 并置 `Overflow=true`。
  128 位精确性对任何物理可表示的输入（点数 < 2^64）都成立。
- **索引访问统计**：每次查询/变更返回 `QueryStats`
  （访问的 x 节点数、Fenwick 读写次数、坐标表重建次数），
  索引另维护累计 `Totals`（`Stats()` / `ResetStats()`）。
- **全扫描核对**：`Scan` 是 O(n) 全扫描参考实现，与 `Query` 使用完全相同的
  半开边界与溢出规则；随机化测试对数千次增删改后的随机矩形逐一比对两者结果。

## API 摘要

```go
ix := sparse2d.New()
err := ix.Add(sparse2d.Point{ID: "a", X: 1, Y: 2, W: -5})
err  = ix.Replace(sparse2d.Point{ID: "a", X: 9, Y: 9, W: 7}) // 原子
err  = ix.Remove("a")
res, err := ix.Query(sparse2d.Rect{MinX: 0, MinY: 0, MaxX: 10, MaxY: 10})
// res.Count, res.Sum, res.Overflow, res.Stats
ref, _ := ix.Scan(rect) // 全扫描参考
tot := ix.Stats()       // 累计访问统计
```

## 运行

```sh
go test ./sparse2d/ -v   # 边界、负权重、极大稀疏坐标、溢出、随机全扫描核对
go run ./cmd/demo        # 演示并打印索引访问统计
```
