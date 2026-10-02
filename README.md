# hexgrid — 离散六边形分层空间索引与 K 环拓扑检索（零分配缓存/对象池）

基于二十面体展开的离散六边形地理网格系统（类 H3 架构），纯 Go 标准库实现。

## 功能

- **经纬度 → 分层网格编码**：二十面体 20 面 gnomonic 投影 + IJK 六边形坐标，
  64 位 `Cell` 索引（5bit 面 / 4bit 分辨率 / 27+27bit 轴向坐标），
  渲染为 16 位十六进制字符串，分辨率 0–15。
- **父子单元缩放**：`Parent(res)` / `Children(res)`，几何层级（每层 √7 缩放）。
- **无浮点三角计算的 K-Ring 遍历**：`GridDisk` / `GridRing` 在整数 IJK 格上
  螺旋行走；面内走纯整数快速路径（零浮点），跨面时仅用预算基向量做点积
  重投影（无三角函数）；Voronoi 归属 + 确定性平局裁决保证编码全局稳定。
- **GC 停顿消除**：`GridDiskInto`/`GridRingInto` 热路径 0 堆分配；
  `zerocache.SlicePool` 定容对象池（Get/Put 零分配）+
  `zerocache.Cache` 分片 FIFO 缓存（命中零分配），
  `DiskCache` 将 K 环结果缓存命中延迟降低约一个数量级。

## 运行样例

```sh
export GO111MODULE=on
go run ./cmd/demo
```

输出（北京 GPS → res 8 编码 → 3 环邻居 → 覆盖验证 → 缓存延迟对比）：

```
GPS (39.9042, 116.4074) @ res 8
  cell:   2440007d180007a0  (face 4)
  center: 39.907435, 116.407269
  parent@5: 22c00006c800006a  children@9: 7

k=3 disk: 37 cells (1+3k(k+1)=37)
  ring1[0] = 2440007d280007a2
  ...
coverage: closed=true holes=0

latency (200000 rounds, k=3):
  direct GridDiskInto: 27301 ns/op
  cached hit path:     2195 ns/op
```

## 测试

```sh
go test ./...          # 单元测试（含零分配断言 AllocsPerRun == 0）
go test -race ./...    # 并发安全 + 严格无泄漏断言（InUse==0, gets==puts+discarded）
go test -bench . -benchmem .
```

基准（darwin/amd64）：`CellFromLatLng` 3.0µs、`GridDiskInto k=3` 6.3µs（面内）
/ 94.7µs（跨面）、缓存命中 0.84µs，**全部 0 allocs/op**。

## 设计说明

- **编码布局**：`[63:59] face | [58:55] res | [54:28] q | [27:1] r | [0] 0`，
  (q,r) 为轴向坐标（q=i−k, r=j−k），与归一化 IJK 一一对应。
- **跨面归属**：单元中心按 Voronoi 规则归属最近面心所在面；二十面体棱中点
  等距平局按最小面编号确定性裁决；`localize` 迭代至不动点/环并按环内最小
  面编号收敛，保证 `encode(LatLng(cell)) == cell` 处处成立。
- **已知限制**：面接缝处相邻面网格独立，接缝附近单元为近似拼接（与 H3 的
  基单元表方案不同）；十二面体顶点（五边形位置）附近 K 环数量可能略少于
  1+3k(k+1)。面内区域严格满足封闭无空洞。
