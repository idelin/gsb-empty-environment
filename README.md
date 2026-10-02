# hexgrid — 离散六边形分层空间索引与 K 环拓扑检索（Go）

基于二十面体展开的离散六边形地理网格系统（类 H3 的轻量实现），仅使用
Go 标准库。核心目标：**高频调用路径零堆分配**，通过内存复用与对象池化
消除 GC 停顿。

## 设计

### 二十面体网格
- 12 顶点（两极 + 两个五环）、20 个三角面，内接单位球。
- 经纬度 → 单位向量 → 球心投影（gnomonic）：按面心法向量最大点积选面，
  投影到面平面得到二维坐标。选面/投影/反投影全程只用点积与叉积。
- 每个面上铺 pointy-top 轴向坐标 (q, r) 六边形格。

### 分层编码（aperture-7）
- 每细化一级应用整数线性映射 `M = [[3,1],[-1,2]]`（det = 7），即旋转
  ≈19.1° + 缩放 √7；每个父单元有 7 个子单元（中心 + 6 邻居）。
- 单元地址 = 根偏移（每面 7 个根六边形）+ 每级一个 3 bit 数字，
  数字是 `Z²/M·Z²` 的 7 个剩余类。地址 ⇄ 轴向坐标的换算（`reduceAxial` /
  `buildAxial`）是**纯整数精确运算**。
- 64 位索引布局（H3 风格）：`mode(4) | res(4) | baseCell(8) | 15×3bit digits | reserved(3)`，
  共 140 个基单元（20 面 × 7 根），分辨率 0–15。

### 无浮点三角的 K-Ring 遍历
- 同面遍历：轴向坐标系下的立方体距离环生成，**纯整数**，无浮点、无三角、
  无分配（结果写入调用方缓冲）。
- 跨面回退：邻域越出面边界时，经球心投影把单元中心重新锚定到相邻面
  （点积 + 一次 sqrt，仍无三角函数）。

### 零分配缓存与对象池
- `Cache`：64 分片 × 8 路固定容量 uint64→uint64 缓存，近似 LRU 驱逐，
  无 map、无切片、Get/Put 零分配、内存占用恒定，并发安全。
- `BufferPool`：基于 `sync.Pool` 的定长 `RingBuffer`（128 单元，支持
  k ≤ 6 的完整 disk）对象池，带 gets/puts 计数，可断言严格无泄漏。

## 目录

| 文件 | 内容 |
|---|---|
| `vec3.go` | 单位球面向量运算 |
| `icosa.go` | 二十面体顶点/面、球心投影 |
| `axial.go` | 轴向坐标、aperture-7 整数层级变换 |
| `cell.go` | 64 位单元索引、十六进制编解码、父子缩放 |
| `index.go` | 经纬度 ⇄ 单元编码 |
| `kring.go` | K-Ring / GridDisk / 拓扑距离（无三角热路径） |
| `cache.go` | 分片定容零分配缓存 |
| `pool.go` | RingBuffer 对象池与缓冲版 K-Ring |
| `cmd/demo` | 端到端演示 |

## 运行

```sh
go test ./...          # 单元测试（含边界条件、并发、无泄漏断言）
go test -race ./...    # 竞态检测
go test -bench . -benchmem
go run ./cmd/demo      # 运行样例
```

## 运行样例（`go run ./cmd/demo`）

```
GPS (39.9042, 116.4074) @ res 8  ->  181c818136fffff8
cell center                 ->  (39.908622, 116.414497)
parent chain (zoom out):
  res  8: 181c818136fffff8
  res  7: 171c818137fffff8
  ...
  res  0: 101cfffffffffff8
children of res-4 parent 141c818ffffffff8: 7 cells, first = 151c8181fffffff8

k=3 grid disk: 37 cells (1+3k(k+1) = 37 expected)
k=3 hollow ring: 18 cells (6k = 18 expected)

cache hit: 181c818136fffff8 (capacity 512, len 1)

zero-allocation proof (allocs/op):
  LatLngToCell:        0
  GridDiskBuffered k=3: 0
  Cache.Get (hit):      0

encode throughput: 200000 ops in 96.251541ms (481 ns/op)
k-ring throughput: 20000 ops in 197.517083ms (9876 ns/op)

pool stats: gets=3 puts=3 outstanding=0 (0 = no leak)
```

## 测试结果

`go test ./...`：33 个测试全部通过（含 `-race`）：

- 编码：金样向量 `181c818136fffff8`、字符串往返、极点/日界线、分辨率钳制
- 层级：父子前缀一致性、7 子单元互异且回归父单元
- K-Ring：k=0..4 数量计数 1/7/19/37/61、环计数 6k、**无空洞**（环 d 每格
  与环 d-1 相邻）、**闭合**（环上每格恰有 2 个同环邻居）、跨面边界、并发一致
- 缓存：命中/更新/驱逐有界、key 0 保留、量化键、**0 allocs/op**、并发（-race）
- 对象池：gets==puts 严格平衡、nil 忽略、缓冲复用、容量溢出保护、
  **0 allocs/op**、16 goroutine × 5000 次无泄漏、无 goroutine 泄漏

基准（darwin/amd64，全部 0 allocs/op）：

```
BenchmarkLatLngToCell-10       1000000    4153 ns/op    0 B/op   0 allocs/op
BenchmarkGridDiskBuffered-10     10000   58978 ns/op    0 B/op   0 allocs/op
BenchmarkCacheGetHit-10        1000000     299 ns/op    0 B/op   0 allocs/op
BenchmarkCachePut-10           5000000     216 ns/op    0 B/op   0 allocs/op
BenchmarkPoolGetPut-10         3000000     146 ns/op    0 B/op   0 allocs/op
```

## 已知近似（lite 版取舍）

- 根六边形有意超出面边界以保证全覆盖；跨面单元的中心重编码会落到相邻
  面的等价重叠单元（与 H3 基单元重叠区概念一致），测试对此显式断言。
- 跨面拓扑距离未实现（`HexDistance` 返回 `ok=false`）。
