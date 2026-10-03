# delaunay

二维散点集 Delaunay 三角网构建器（仅使用 Go 标准库）。

## 特性

- **增量插入**：逐点动态插入，点定位采用 walk 算法（带线性扫描兜底）。
- **Lawson 翻边**：每次插入后在局部翻转非法边，恢复空外接圆性质
  （Empty Circumcircle Property）。
- **紧凑拓扑输出**：每个三角形记录 3 个顶点索引（逆时针）与 3 个邻接
  邻居索引（`-1` 表示凸包边界），外边界以逆时针凸包顶点环输出。
- **正确性保障**：超三角形顶点若落入近似退化三角形的外接圆内，会导致
  凸包覆盖缺损；构建器会自动放大超三角形并重建，直到网格边界被证明
  等于点集凸包。

## 结构

- `delaunay.go` — 三角化器（`Triangulator` / `Insert` / `Mesh`）与拓扑提取
- `geom.go` — 几何谓词（`orient2d`、`inCircle`、`Circumcircle`）
- `delaunay_test.go` — `go test` 验收测试
- `cmd/demo` — 50 个随机测绘点的运行样例

## 使用

```go
pts := []delaunay.Point{{0, 0}, {10, 0}, {10, 10}, {0, 10}}
mesh := delaunay.Triangulate(pts)
// mesh.Triangles[i].V — 顶点索引（CCW）
// mesh.Triangles[i].N — 每条对边的邻居三角形索引，-1 为边界
// mesh.Hull — 凸包顶点索引（CCW）
```

## 运行样例

```
$ go run ./cmd/demo
input points        : 50
triangles           : 87
convex hull (11 pts) : [17 22 10 15 30 29 13 48 7 6 42]
circumcircle violations: 0
angle range         : 2.12 deg .. 174.17 deg
Euler check         : T=87, 2N-2-H=87

sample of compact topology (first 5 triangles):
  T0  vertices=[37 11 0] neighbours=[35 63 2]
  T1  vertices=[29 13 0] neighbours=[63 54 -1]
  T2  vertices=[37 1 11] neighbours=[7 0 64]
  T3  vertices=[36 18 3] neighbours=[29 8 62]
  T4  vertices=[46 43 3] neighbours=[75 81 80]
```

## 测试结果

```
$ go test ./... -v
=== RUN   TestEmptyCircumcircle      # 50 随机点：空外接圆性质 + 邻接 + 凸包
--- PASS: TestEmptyCircumcircle (0.00s)
=== RUN   TestEulerFormula           # T = 2N - 2 - H
--- PASS: TestEulerFormula (0.00s)
=== RUN   TestIncrementalInsertion   # 逐点插入，每步验证空外接圆与邻接
--- PASS: TestIncrementalInsertion (0.00s)
=== RUN   TestSquare                 # 正方形最小用例
--- PASS: TestSquare (0.00s)
=== RUN   TestPointOnEdge            # 点恰好落在边上（split4 路径）
--- PASS: TestPointOnEdge (0.00s)
=== RUN   TestDuplicatePoints        # 重复点去重
--- PASS: TestDuplicatePoints (0.00s)
=== RUN   TestRandomSets             # 10/30/50/100/200 点多种子扫描
--- PASS: TestRandomSets (0.07s)
=== RUN   TestAngleQuality           # 网格均匀性（最小角统计）
--- PASS: TestAngleQuality (0.00s)
PASS
ok  	delaunay	0.392s
```
