# geocell — 离散六边形分层空间索引与 K 环拓扑检索 + 局部性感知路由引擎

纯 Go 标准库实现。基于二十面体展开的离散六边形地理网格（类 H3 的孔径-7
分层体系），以及面向超大规模拓扑的虚拟分片与局部性感知调度引擎。

## 模块结构

```
grid/    二十面体离散六边形网格核心
  geo.go        二十面体(顶点->面自动枚举)、gnomonic 投影、经纬度<->面坐标
  ijk.go        整数 IJ 格网坐标、孔径-7 上/下变换(纯整数)、层级数字提取
  cell.go       64 位单元索引位布局、十六进制编解码、经纬度->单元
  hierarchy.go  父子单元缩放(Parent/Children，纯整数)
  kring.go      无浮点三角计算的拓扑 K-Ring 邻域遍历
shard/   分层分片与局部性感知路由引擎
  shard.go      虚拟分片(分片=祖先单元，天然地理连续)
  ring.go       一致性哈希环(虚拟节点 + splitmix64 混合)
  router.go     单元->节点路由、按分片批量分组
  engine.go     actor 模型并发调度引擎、指标、优雅关闭
cmd/demo/       验收场景运行样例
```

## 索引位布局（64 位）

```
bits 63-59  mode (1 = cell)
bits 58-56  reserved (0)
bits 55-52  resolution (0-14)
bits 51-45  icosahedron face (0-19)
bits 44-0   15 x 3bit 层级数字; digit[0] 为 res-0 前导数字, 未用位=7
```

## 关键设计

- **编码**：经纬度 -> 单位向量 -> 最大点积选面 -> gnomonic 投影 ->
  分辨率相关旋转/缩放 -> 六边形量化。始终在 MaxRes 量化后截断数字，
  因此 `Parent(LatLngToCell(p,r),q) == LatLngToCell(p,q)` 严格成立。
- **层级**：孔径-7，奇数层用旋转 +40.89° 的变换、偶数层 +19.11°，
  两层复合 60° 使格网重新对齐；上/下变换均为行列式 7 的整数矩阵 +
  整数立方坐标舍入。
- **K-Ring**：中心单元解码为整数 IJ，叠加立方坐标环偏移，重新编码。
  遍历全程零浮点、零三角函数；`1+3k(k+1)` 个单元。
- **分片**：分片即 shardRes 层的祖先单元，拓扑邻居绝大多数同分片；
  一致性哈希把分片映射到节点，节点增删只迁移受影响分片。
- **调度**：K 环查询本地计算后按分片批量路由，37 个单元的 3 环查询
  通常只需 1~2 次跨节点 RPC（样例中 -97.3%）。节点为 actor 模型
  （单 goroutine 独占本地存储，热路径无锁），关闭幂等、无 goroutine 泄漏。

## 运行样例

```
$ go run ./cmd/demo
[1] GPS (39.9042, 116.4074) -> res=8 cell 880810bb203ffff
    face=4  center=(39.893164, 116.407457)  valid=true
    parent(res=7) = 870810bb21fffff
    parent(res=6) = 860810bb2ffffff
    parent(res=5) = 850810bb7ffffff
    children(res=9) = 7 cells, first = 890810bb2007fff

[2] K-ring k=3: 37 cells (1+3k(k+1)=37), face-local, no floats in traversal
    coverage check: interior cells fully surrounded, holes=0 -> continuous & closed
    sample cells: 880810bb28fffff 880810bb277ffff 880810bb263ffff ...

[3] locality-aware k-ring query over 4 nodes:
    ring cells=37  shards touched=1  nodes touched=1
    cross-node RPCs=1  (naive cell-at-a-time=37, -97.3%)
    present in cluster: 37/37 ring cells
    engine metrics: queries=1 ingests=1 cellsRouted=20074 crossNodeRPC=7589 cellsHit=37
    engine shut down cleanly, no leaked goroutines
```

## 测试

```
$ go test ./... -race -count=1
ok  	geocell/grid
ok  	geocell/shard
```

覆盖：编解码往返(2000 随机点 x 全分辨率)、极点/日界线、父子一致性
(3000 随机点)、子单元枚举、十六进制往返与非法输入、K 环尺寸/唯一性/
闭合无空洞(验收边界: res=8, k=3)/几何连续性/整数往返、分片局部性、
一致性哈希确定性与最小迁移、批量路由分组、引擎并发(-race, 40 goroutine
混合读写)、并发下查询正确性、幂等关闭、严格无 goroutine 泄漏断言。

## 已知边界

- K 环遍历在跨越二十面体边界时返回 `ErrFaceCrossing`（res-8 下单个面
  跨度约 70°，3 环仅约 0.04°，仅当中心距面边界 <0.02° 时触发）；
  `ValidateFaceLocal` 提供几何归属校验。
