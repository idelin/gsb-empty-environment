# hexgrid — 离散六边形分层空间索引与 K 环拓扑距离检索（含形式化约束巡检与不变式断言仪）

基于二十面体展开的离散六边形地理网格系统（Go，仅标准库）。提供：

- **经纬度 → 分层网格编码**：球面划分到二十面体的 20 个面，每个面在切平面上做球心投影（gnomonic），量化为轴向六边形坐标 `(face, i, j)`，打包成 64 位单元 ID，渲染为十六进制字符串。
- **父子单元缩放**：孔径 4（aperture-4）层级，每单元恰有 4 个子单元、1 个父单元，全部为精确位运算。
- **无浮点三角计算的拓扑 K-Ring 遍历**：同面内为纯整数轴向格 BFS；跨面时只做向量代数（点积/归一化）的重投影，**不使用任何三角函数**。邻接关系通过"反向闭包"扫描构造成严格对称图。
- **离线完整性巡检仪（Inspector）**：对全量状态快照主动发现悬挂引用、孤立环路、数据倾斜。
- **不变式断言仪（Asserter）**：形式化校验编码、层级、K 环拓扑的不变式，返回逐条违规报告。

## 单元 ID 位布局

```
63..60  mode = 1        59..56  resolution (0..15)
55..51  face (0..19)    50..0   index = i*2^res + j
```

## 构建与运行

```bash
go build ./...
go run ./cmd/hexdemo   # 运行样例
go test ./...          # 单元测试（含边界条件与并发安全）
go test -race ./...    # 竞态检测
```

## 运行样例（`go run ./cmd/hexdemo`）

```
GPS (39.9042, 116.4074) @ res 8 -> cell 1800000000009457
cell center: (39.902304, 116.434196), face 0
k=3 ring: 37 cells (closed-form expectation 37)
  ring invariants: unique, contiguous, closed, no holes
parent @ res 7: 170000000000252b, children: 4, contains original cell: true
inspect clean universe (res 3+4, 6400 cells): ok=true, components=1, skew max/mean=1.00
inspect corrupted snapshot: ok=false, findings=6
  [dangling-ref] cell 1400000000000011: ancestor 1300000000000000 at res 3 is missing
  [dangling-ref] cell 1400000000000010: ancestor 1300000000000000 at res 3 is missing
  [dangling-ref] cell 1400000000000001: ancestor 1300000000000000 at res 3 is missing
  [dangling-ref] cell 1400000000000000: ancestor 1300000000000000 at res 3 is missing
  [orphan-loop] cell 140000000000003e: isolated component of 7 cells at res 4 detached from main component of 5101 cells
  [data-skew] cell 1350000000000000: bucket weight 10002 exceeds 3.0x the mean 11.80
verify universe res 4: cells=5120 reachable=1280 ok=true
```

验收边界：GPS `(39.9042, 116.4074)` 编码为 Resolution=8 的十六进制字符串
`1800000000009457`；其半径 3 环恰含 `1 + 3·3·4 = 37` 个单元，经断言仪校验
唯一、连续、闭合、无空洞遗漏。

## 测试结果（`go test -v ./...`，另以 `-race` 复跑通过）

```
--- PASS: TestLatLngToCellGolden (0.00s)        # 验收：res-8 十六进制编码金样
--- PASS: TestParseRoundTrip (0.00s)            # 十六进制解析往返
--- PASS: TestInvalidInputs (0.00s)             # 非法分辨率/纬度/经度/NaN
--- PASS: TestBoundaryCoords (0.00s)            # 两极、对映子午线、赤道、全分辨率
--- PASS: TestParentChildScaling (0.00s)        # 父子缩放、MaxRes 边界
--- PASS: TestIsValidCorrupted (0.00s)          # 损坏的 mode/face/index 位
--- PASS: TestUniverse (0.00s)                  # 全量宇宙唯一性与合法性
--- PASS: TestHierarchySweep (0.00s)            # res-4 全域层级不变式
--- PASS: TestConcurrentSafety (9.83s)          # 16 协程校验和与串行一致
--- PASS: TestNoGoroutineLeak (0.07s)           # 严格无 goroutine 泄漏
--- PASS: TestConcurrentResultIndependence (0.04s) # 结果独立所有权，无共享状态
--- PASS: TestInspectClean (0.18s)              # 干净快照零发现
--- PASS: TestInspectDanglingRef (0.18s)        # 注入缺失父单元 → 4 条悬挂引用
--- PASS: TestInspectOrphanLoop (0.62s)         # 切断外壳 → 检出孤立环路
--- PASS: TestInspectDataSkew (1.02s)           # 注入热点 → 检出数据倾斜
--- PASS: TestInspectSkewThreshold (3.21s)      # 倾斜阈值边界
--- PASS: TestVerifyUniverse (0.01s)            # 全域审计：唯一/合法/孔径4覆盖
--- PASS: TestAssertCellValid (0.00s)
--- PASS: TestAssertHierarchyLevels (0.00s)
--- PASS: TestAssertEncodeRoundTrip (0.00s)
--- PASS: TestExpectedKRingSize (0.00s)         # 1+3k(k+1) 闭式
--- PASS: TestKRingAcceptance (0.00s)           # 验收：k=3 环 37 单元闭合无洞
--- PASS: TestKRingSizesInterior (0.00s)        # 1/7/19/37 尺寸序列
--- PASS: TestNeighborsSymmetrySweep (3.89s)    # res 0..4 全域邻接对称性
--- PASS: TestKRingRes0 (0.06s)                 # res-0 跨面环绕、3 步覆盖全球
--- PASS: TestKRingCrossFace (0.00s)            # 跨面环：去重/合法/连续/对称
--- PASS: TestKRingSweepRes4 (6.43s)            # res-4 全部可达单元 k=2 不变式
--- PASS: TestKRingDeterministic (0.00s)        # 遍历确定性与结果新鲜分配
PASS
ok  	hexgrid
```

## 设计说明与已知边界

- **面缝行为**：每个面的方形网格略大于其球面三角形（边缘带为结构合法但
  不可达的"边距单元"）。跨缝邻接通过重投影 + 反向闭包对称化，全部分辨率上
  邻接关系严格对称（测试 `TestNeighborsSymmetrySweep` 逐单元证明）。缝附近
  的环尺寸偏离闭式 `1+3k(k+1)`（投影畸变所致，类似 H3 的五边形畸变），
  断言仪的精确尺寸检查只作用于单面环。
- **res 0**：每面 1 个单元，邻接环绕到相邻面（5–7 个邻居），k=3 覆盖全球
  20 个单元。
- **并发安全**：全部几何表在 `init` 中构建后只读，所有 API 无共享可变状态；
  `-race` 通过，且测试断言无 goroutine 泄漏、结果切片独立所有。
