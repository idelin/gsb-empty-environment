# rtree — 二维 R-Tree 空间索引（Go 标准库实现）

地理信息空间矩形 R-Tree 索引器：支持多边形外包矩形与经纬度点插入、
矩形范围窗口查询（Bounding Box Query）。节点溢出时采用
**重插入（Re-Insert）** 与 **二次分裂（Quadratic Split）** 算法，
优化空间覆盖重叠度，遏制树退化。仅依赖 Go 标准库。

## 结构

- `rtree/rtree.go` — R-Tree 核心：`Rect`/`Point` 几何类型、`Insert`、`Search`、
  重插入、二次分裂、AdjustTree 边界向上调整。
- `rtree/rtree_test.go` — 正确性与结构不变量测试、重插入对照实验、基准。
- `cmd/demo/main.go` — 运行样例：5 万家商铺经纬度建索引 + 视野窗口查询。

## 算法要点

- **ChooseLeaf**：逐层选择面积扩张最小（平局取面积最小）的子树；
  节点显式记录层级（叶子=1），根分裂长高后插入位置依然正确。
- **溢出处理（OverflowTreatment）**：同一层在一次插入中首次溢出时，
  移除离节点中心最远的 30% 条目并重新插入（Re-Insert），
  促使条目在兄弟间重新分布；该层再次溢出才执行二次分裂。
- **Quadratic Split**：PickSeeds 挑选死空间最大的一对种子，
  PickNext 按偏好差异最大逐条分配，保证最小填充率。
- **AdjustTree**：插入/重插入/分裂后沿父链重算祖先条目外包矩形，
  保证查询剪枝的精确性。

## 使用

```go
tree := rtree.New(8, 3) // 节点容量 8，最小填充 3
tree.Insert(shopID, rtree.PointRect(rtree.Point{X: lon, Y: lat}))
hits := tree.Search(rtree.NewRect(rtree.Point{X: 121.45, Y: 31.20},
                                  rtree.Point{X: 121.50, Y: 31.25}))
```

## 运行

```sh
go run ./cmd/demo      # 运行样例
go test ./rtree/ -v    # 运行测试
go test ./rtree/ -run xxx -bench .  # 基准
```

## 测试结果（Go 1.11, darwin/amd64）

```
--- PASS: TestRectBasics
--- PASS: TestInsertAndSearchMatchesBruteForce   (5000 商铺 × 220 窗口 vs 暴力扫描)
--- PASS: TestPolygonBoundingBoxes               (2000 多边形外包矩形 × 100 窗口)
--- PASS: TestTreeInvariants                     (20000 点: 树高=6, 填充率/同层叶子/父子 bbox 一致)
--- PASS: TestReinsertReducesOverlap             (重插入: 重叠 0.0595 vs 禁用 0.1542, 节点 4093 vs 4365)
--- PASS: TestEdgeCases                          (空树/重复坐标/单点)
BenchmarkInsert10K-10     673 ms/op
BenchmarkRangeQuery-10    1.65 ms/op   (10 万商铺中窗口检索)
```

运行样例输出：

```
插入 50000 家商铺耗时: ~1.1s (树高=6)
窗口查询: 命中 1640 家商铺, R-Tree 耗时 ~209µs, 暴力扫描耗时 ~444µs
结果与暴力扫描完全一致 ✓
```
