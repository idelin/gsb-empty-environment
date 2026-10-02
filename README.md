# delaunay — 空间拓扑邻接图与 Delaunay 三角剖分器

纯 Go 标准库实现的二维散点集 Delaunay 三角网构建器。采用**逐点动态插入**，
每次插入后在局部应用 **Lawson 翻边算法**恢复合法性，最终输出满足
**空外接圆性质（Empty Circumcircle Property）** 的紧凑三角拓扑网格，
并给出每个三角形的邻接邻居与外边界凸包。

## 算法要点

- **超三角形起步**：构造一个足够大、包含所有点的超三角形作为初始网格。
- **点定位**：对当前三角形做方向测试，找到包含新点的三角形（严格内部）
  或所在边（恰好在边上）。
- **局部分裂**：内部点将三角形一分为三；边上点将共享该边的两个三角形
  一分为四。
- **Lawson 翻边**：对新点对面的边做 in-circle 测试，若邻居三角形的外接圆
  包含新点则翻转四边形对角线，并递归合法化新产生的两条边。
- **超三角形移除与修复**：边界上近共线的三角形外接圆可能极大并触及超
  三角形顶点，导致移除后边界内凹。`Triangulation()` 会检测边界凹陷，
  按最大违规外接圆半径放大超三角形并重建，直到网格边界恰为输入点集
  的凸包。
- **紧凑拓扑输出**：`Triangle{V, N}` —— `V` 为逆时针顶点索引，`N[i]` 为
  `V[i]` 对边上的邻居三角形索引，`-1` 表示凸包边界边。

## 文件结构

```
delaunay.go        核心实现（Builder / Triangulation / Lawson 翻边）
delaunay_test.go   go test 测试（空外接圆、邻接一致性、凸包、Euler 公式等）
stress_test.go     压力测试（60×300 随机点、聚簇+离群点）
cmd/demo/main.go   运行样例：50 个随机测绘点
```

## 运行

```sh
go test ./... -v     # 运行测试
go run ./cmd/demo    # 运行样例
```

## 运行样例（`go run ./cmd/demo`，节选）

```
=== Delaunay 三角剖分演示 ===
输入点数:        50 (种子 42, 均匀分布于 [0,100)²)
三角形数:        89 (Euler 公式 2n-2-h = 89)
凸包顶点 (9, 逆时针): [21 12 13 9 16 45 17 2 29]
三角形邻接表 (V=顶点, N=对边邻居, -1=凸包边界):
  T0   V=[ 7  1  5]  N=[18  6 21]
  T1   V=[21 12  0]  N=[ 7 34 -1]
  T2   V=[49 28  7]  N=[48 45 87]
  T3   V=[38  1 27]  N=[21 46 66]
  ...
  T87  V=[49 33 28]  N=[16  2 12]
  T88  V=[49 37 42]  N=[24 74 45]
空外接圆性质:    PASS (已验证全部 89 个三角形)
```

## 测试结果（`go test ./... -v`）

```
=== RUN   TestEmptyCircumcircleRandom50
--- PASS: TestEmptyCircumcircleRandom50 (0.00s)
=== RUN   TestIncrementalInsertionStaysDelaunay
--- PASS: TestIncrementalInsertionStaysDelaunay (0.00s)
=== RUN   TestGridPointsOnEdges
--- PASS: TestGridPointsOnEdges (0.00s)
=== RUN   TestDuplicatePoints
--- PASS: TestDuplicatePoints (0.00s)
=== RUN   TestCircumcircle
--- PASS: TestCircumcircle (0.00s)
=== RUN   TestManyRandomSeeds
--- PASS: TestManyRandomSeeds (0.00s)
=== RUN   TestStress
--- PASS: TestStress (0.26s)
PASS
ok      delaunay
```

测试覆盖：

- **TestEmptyCircumcircleRandom50**：50 个随机点，验证空外接圆性质、邻接
  互指一致性、凸包包含全部点、三角形数满足 Euler 公式 `T = 2n-2-h`。
- **TestIncrementalInsertionStaysDelaunay**：逐点动态插入，每插入一个点
  都验证全网仍满足空外接圆性质。
- **TestGridPointsOnEdges**：8×8 整数网格，覆盖"点落在边上"的一分为四
  分裂与大量共圆退化情形。
- **TestDuplicatePoints**：重复点去重。
- **TestCircumcircle**：外接圆圆心/半径数值正确性。
- **TestManyRandomSeeds / TestStress**：多种子、300 点规模、聚簇加离群
  点的稳健性验证。
