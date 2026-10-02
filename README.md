# rtree — 二维 R-Tree 空间索引（Go 标准库实现）

地理信息空间矩形 R-Tree 索引器：支持多边形外包矩形（MBR）插入与矩形范围窗口查询
（Bounding Box Query）。节点溢出时先执行 R*-树风格的**强制重插入（Re-Insert）**，
仍溢出则采用 **二次分裂（Quadratic Split）**，以降低节点外接矩形重叠度、遏制树退化。

## 结构

- `rtree.go` — 核心实现：`Rect`、`RTree`（`Insert` / `Search` / `SearchWithStats`）
- `rtree_test.go` — 正确性、剪枝、抗退化、重插入降重叠测试与基准
- `cmd/rtree-demo/main.go` — 运行样例：2 万家商铺经纬度建索引 + 视野窗口检索

## 算法要点

- **ChooseSubtree**：按"面积增量最小、面积最小"选择下落分支。
- **重插入**：某层在一次插入中首次溢出时，移除离节点中心最远的 p=30% 条目重新插入，
  让条目找到更合适的分支，减少分裂次数与重叠。
- **二次分裂**：PickSeeds 选"组合浪费面积最大"的种子对，PickNext 按偏好差最大逐条分配，
  并保证两组至少 40% 填充率。
- **查询剪枝**：窗口与节点外接矩形不相交即整支跳过。

## 使用

```go
tree := rtree.New(16) // 节点最大条目数

// 点（商铺经纬度）
tree.Insert(rtree.PointRect(lng, lat), shop)
// 多边形外包矩形
tree.Insert(rtree.Rect{MinX: x1, MinY: y1, MaxX: x2, MaxY: y2}, polygon)

// 视野窗口查询
hits := tree.Search(rtree.Rect{MinX: 121.40, MinY: 31.18, MaxX: 121.55, MaxY: 31.28})
// 带剪枝统计
hits, visited := tree.SearchWithStats(window)
```

## 运行

```sh
export GO111MODULE=on
go test -v ./...        # 测试
go test -bench . .      # 基准
go run ./cmd/rtree-demo # 样例
```

## 实测结果（Go 1.11, darwin/amd64）

- 插入 20,000 家商铺：约 465ms；树高 3，节点 1,906
- 视野窗口查询：137 条结果，约 33µs，与暴力扫描完全一致
- 剪枝：仅访问 98/1,906 个节点（跳过约 94.9%）
- 重插入 vs 直接分裂：内部节点重叠面积 4108.68 vs 8836.07（降低约 53%）
- 按经度排序的对抗性插入 20,000 条：树高稳定在 4，无退化
- 基准：插入 10k 条约 61ms；窗口查询约 10.7µs/op
