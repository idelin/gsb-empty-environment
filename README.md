# floodsim — 时空栅格动态连通性与洪水蔓延模拟器

纯 Go 标准库实现的时空二维栅格状态演化仿真内核。模拟暴雨时序下地表淹没
区域随时间的动态扩散，综合考虑地形高程（DEM）、土壤渗透率（支持逐像元
渗透率图）与降雨量时态序列；在离散 tick 下可查询任意历史时刻的积水连通
区域边界多边形，以及被水体阻断的道路通行断点列表。

## 结构

```
flood/grid.go    栅格数据结构 (Grid)
flood/sim.go     仿真内核：降雨注入 → 入渗损耗 → 沿坡度漫流松弛 (Simulator)
flood/region.go  8-连通积水区域提取 + Moore 邻域边界多边形追踪
flood/road.go    道路折线采样与淹没断点检测
cmd/floodsim/    运行样例（合成 DEM + 暴雨时序 + 三条道路）
flood/flood_test.go  单元测试与端到端测试
```

## 模型说明

- 每个 tick 依次执行：降雨量注入（时序值）→ 逐像元入渗扣除 → 若干轮
  漫流松弛。漫流按水面高程（DEM + 水深）差向 8 邻域低处转移，水量守恒。
- 水深 ≥ `BlockDepth` 的像元视为淹没，参与连通区域合并并阻断道路。
- 每个 tick 保存不可变快照，`DepthAt/RegionsAt/BlockagesAt` 可查询任意
  历史时刻。
- 边界多边形采用 Moore 邻域追踪（Jacob 停止准则 + 状态去重），输出栅格
  坐标下的闭合顶点环。
- 道路以折线表示，按 0.25 像元步长采样，每段连续淹没区间输出一个断点
  （进入点/退出点/淹没像元数）。

## 运行

```sh
go run ./cmd/floodsim
go test ./flood/ -v
```

## 运行样例输出（节选）

```
=== Spatiotemporal flood spread simulation ===
grid 20x12, rain series [0.1 0.2 0.4 0.5 0.4 0.25 0.15 0.05 0 0 0 0]

tick  water      regions  blocked roads
1     3.60       0
2     25.20      3        LakeRoad[8.0->11.8] LakeRoad[19.0->19.0] HillRoad[19.0->19.0] DiagRoad[8.1->11.9]
3     94.80      2        LakeRoad[8.0->11.8] LakeRoad[17.0->19.0] HillRoad[17.0->19.0] DiagRoad[8.1->11.9] DiagRoad[16.4->17.0]
4     188.40     1        LakeRoad[8.0->11.8] LakeRoad[13.0->19.0] HillRoad[14.0->19.0] DiagRoad[8.1->11.9] DiagRoad[13.6->17.0]
5     258.00     1        LakeRoad[8.0->19.0] HillRoad[11.0->19.0] DiagRoad[7.9->17.0]
...
12    233.84     1        LakeRoad[8.0->19.0] HillRoad[12.0->19.0] DiagRoad[8.1->11.9] DiagRoad[13.1->17.0]

=== Connected regions at final tick 12 ===
region #0: area=111 cells, boundary polygon (42 vertices): [{13 0} {14 0} ... {12 1} {13 0}]

=== Historical query: breakpoints at every tick ===
tick  0: all roads passable
tick  1: all roads passable
tick  2: LakeRoad blocked from (8.00,5.50) to (11.75,5.50), 4 cells
tick  2: DiagRoad blocked from (8.08,6.74) to (11.93,4.05), 6 cells
...
```

可见暴雨增强期（tick 2–5）盆地与东侧低地两个积水区逐渐扩张并合并为
单一连通区域，三条道路相继被切断；雨停后（tick 8+）入渗占优、水体缓慢
消退。

## 测试结果

```
$ go test ./flood/ -v
=== RUN   TestGridBasics
--- PASS: TestGridBasics (0.00s)
=== RUN   TestRainAndInfiltration
--- PASS: TestRainAndInfiltration (0.00s)
=== RUN   TestWaterConservation
--- PASS: TestWaterConservation (0.00s)
=== RUN   TestFlowDownhill
--- PASS: TestFlowDownhill (0.00s)
=== RUN   TestPerCellInfiltrationMap
--- PASS: TestPerCellInfiltrationMap (0.00s)
=== RUN   TestRegionConnectivity
--- PASS: TestRegionConnectivity (0.00s)
=== RUN   TestBoundaryPolygonSquare
--- PASS: TestBoundaryPolygonSquare (0.00s)
=== RUN   TestBoundaryPolygonSingleCell
--- PASS: TestBoundaryPolygonSingleCell (0.00s)
=== RUN   TestBoundaryPolygonThinLineTerminates
--- PASS: TestBoundaryPolygonThinLineTerminates (0.00s)
=== RUN   TestRoadBlockage
--- PASS: TestRoadBlockage (0.00s)
=== RUN   TestRoadBlockageTwoSegments
--- PASS: TestRoadBlockageTwoSegments (0.00s)
=== RUN   TestHistoricalSnapshots
--- PASS: TestHistoricalSnapshots (0.00s)
=== RUN   TestEndToEndFloodBlocksRoad
--- PASS: TestEndToEndFloodBlocksRoad (0.00s)
PASS
ok  	floodsim/flood
```
