# hexgrid — 离散六边形分层空间索引 + 形式化约束巡检与不变式断言仪 (Go)

基于二十面体展开的离散六边形地理网格系统，仅使用 Go 标准库。包含三部分：

1. **分层空间索引**：经纬度 → 分层网格编码（64 位索引 / 十六进制字符串），
   父子单元缩放（孔径 7：1 中心子单元 + 6 环位子单元）。
2. **拓扑 K-Ring 检索**：纯整数 IJ 格点代数，**无浮点三角计算**，
   半径 k 环精确返回 `1 + 3k(k+1)` 个单元，连续闭合、无空洞。
3. **离线完整性巡检仪 + 不变式断言仪**：全量状态巡检，主动发现
   悬挂引用、孤立环路、数据倾斜与索引不变式破坏。

## 设计要点

**索引位布局（64 位）**

```
bits 59..63  保留(0)   bits 52..55  分辨率 0..15
bits 56..58  mode=1    bits 47..51  基础单元(二十面体面) 0..19
bits 45..46  保留(0)   bits  0..44  15 个 3-bit 数字位(0..6，未用=7)
```

**孔径 7 整数代数**：子单元 IJ 坐标由父单元经整数线性映射
`M(i,j) = (2i-j, i+3j)`（det=7，即乘以范数为 7 的六边形格代数整数 `2+ω`）
加数字偏移得到。`{中心} ∪ {6 个单位偏移}` 构成模 M 的完全剩余系，
因此每个整数 IJ 点都有唯一数字展开 —— 同分辨率单元构成完整六边形格，
K-Ring 遍历（环角起步 + 6 边单位步进）与 IJ↔数字互转全部为精确整数运算。

**二十面体展开**：12 顶点 / 20 面在初始化时程序化枚举（确定性排序），
经纬度经球面 → 最近面心 → 球心(gnomonic)投影 → 该面切平面六边形格
量化（立方坐标舍入）得到编码；每降一级分辨率缩放 `1/√7` 并旋转
`-19.1066°`，与整数映射 M 严格对偶。

**已知简化**（相对 H3）：无顶点五边形；基础单元覆盖域为面内接圆附近的
花形区域，二十面体顶点附近的点经由贪心最近子中心回退路径编码（全球
任意坐标均可编码，API 不报错）；K-Ring 不跨基础单元，越界时返回
`ErrFaceBoundary`。

## 目录结构

```
cell.go       64 位索引编码、十六进制字符串、父子缩放、位级不变式
ij.go         纯整数 IJ 格代数（M 变换、剩余系、IJ↔数字）
globe.go      二十面体顶点/面枚举、面邻接、切平面基
project.go    经纬度↔单元编码（gnomonic 投影 + 格点量化）
ring.go       K-Ring 遍历、邻接、环距离、闭合覆盖验证
store.go      并发安全单元注册表（RWMutex + 深拷贝，无内部状态泄漏）
inspector.go  离线完整性巡检仪（悬挂引用/孤立环路/数据倾斜/不变式）
invariants.go 不变式断言仪（索引/层级/K-Ring 全项断言）
cmd/demo      验收流程运行样例
```

## 运行样例

```bash
$ go run ./cmd/demo
== 1. Encode GPS at resolution 8 ==
gps=(39.9042, 116.4074) -> cell=1888410e95fffff (base=17 res=8 digits=[1 0 1 0 3 5 1 2])
cell center = (39.901587, 116.414321)

== 2. K-ring radius 3 ==
k-ring(3) cells = 37 (want 1+3*3*4 = 37)
distance histogram: ring0=1 ring1=6 ring2=12 ring3=18
invariants: cardinality/uniqueness/completeness/contiguity/symmetry OK
coverage: continuous, closed, single boundary loop, no holes
first ring-1 neighbors: [1888411347fffff 188841133bfffff 1888411339fffff]

== 3. Parent/child scaling ==
cell   1888410e95fffff (res 8)
parent 1788410e9ffffff (res 7), 7 children, contains cell: true

== 4a. Inspector: clean store ==
records=37 components=1 main-component=37 skew-ratio=20.00 violations=0
verdict: OK=true

== 4b. Inspector: planted anomalies ==
records=150 components=22 main-component=127 skew-ratio=17.07 violations=3
  [dangling-reference] cell=1848492493fffff  parent 1710db6dbffffff referenced but absent from store
  [isolated-loop     ] cell=1850000001fffff  component of 3 cells (closed loop) detached from main component of 127
  [data-skew         ] cell=1888410cc3fffff  base cell 17 holds 128/150 records (17.1x mean 7.5)
verdict: OK=false (false expected: anomalies were planted)

ALL CHECKS PASSED
```

## 测试结果

```bash
$ go vet ./... && go test -race -count=1 -v ./...
```

41 个测试全部通过（race detector 启用），覆盖：

- **编码/解码**：位布局往返、十六进制字符串解析、非法位模式拒绝、
  分辨率/基础单元/数字越界、极点与日界线、全球 5000 随机点覆盖、
  2000 随机单元中心重编码稳定性、金色向量（北京 res8 = `1888410e95fffff`）。
- **层级**：父子往返、7 子单元互异、res 0 / res 15 边界。
- **K-Ring**：`k=0..5` 基数公式、唯一性、完备性（无空洞）、连续性
  （每单元有更近邻）、对称性、闭合覆盖单边界环、带洞/混合分辨率/
  空集拒绝、越界 `ErrFaceBoundary`、负半径、非法中心。
- **巡检仪**：干净/空/均衡仓库通过；悬挂父引用、悬挂邻接、孤立闭环、
  孤立碎片、数据倾斜、3 类不变式破坏均被主动发现；报告确定性。
- **并发与泄漏**：8 goroutine 混合读写 + 巡检（`-race`）、16 goroutine
  纯函数 K-Ring 一致性、goroutine 严格无泄漏（前后计数相等）、
  Store 深拷贝无内部状态别名泄漏。

```
--- PASS: TestAcceptanceKRing3        --- PASS: TestInspectorIsolatedLoop
--- PASS: TestKRingInvariantBattery   --- PASS: TestInspectorDataSkew
--- PASS: TestLatLngToCellGolden      --- PASS: TestStoreConcurrentAccess
--- PASS: TestParentChildHierarchy    --- PASS: TestNoGoroutineLeak
... (41 tests total)
PASS
ok  	hexgrid	2.813s
```
