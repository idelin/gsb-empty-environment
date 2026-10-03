# hexgsb — 离散六边形分层空间索引与 K 环拓扑检索 / 分层分片与局部性感知路由引擎

基于二十面体展开的离散六边形地理网格系统（Go，仅标准库）。

## 设计要点

**`hexgrid` — 分层六边形网格**

- 经纬度 → 单位球面向量 → 球心投影（gnomonic）到二十面体某个面 → 面内
  轴坐标（axial）六边形晶格取整，得到 64 位分层编码：
  `[mode:4][res:4][face:5][q:25][r:25]`，规范形式为 16 位十六进制字符串。
- 晶格常数取 `2^(1-res)`（棱长为 2），整除每条二十面体棱，因此 20 个面的
  晶格展开后拼成**一张全局一致的六边形晶格**；恰好落在面边界上的单元由
  最小面编号规范持有，保证跨面单元身份唯一、邻接关系对称。
- 分辨率每升一级晶格常数减半（aperture-4，平均每单元 4 个子单元），
  `Parent`/`Children` 通过质心重编码定义，跨面一致。
- **K 环遍历无浮点三角计算**：邻居 = 轴坐标整数方向步进（6 个方向），
  仅当跨面时才做一次投影取整；K 环按 BFS 逐环扩张，覆盖连续闭合、
  无空洞遗漏（内部盘精确满足 `1+3k(k+1)`，外环 `6k`），并自然处理
  12 个二十面体顶点处的五边形缺陷（邻居数退化为 5）。

**`shard` — 虚拟分片与局部性感知路由**

- 单元 → 以其 `shardRes` 层祖先为局部性键，FNV-1a 散列到固定数量的
  虚拟分片（2 的幂），拓扑邻近的单元几乎总落在同一分片。
- 虚拟分片 → 物理节点用 rendezvous（HRW）哈希，并对散列值做
  splitmix64 终混（弥补 FNV 高位雪崩不足）；节点增删只迁移约 `1/N`
  的分片。
- `Route` 把一批单元按节点分组；`LocalityScore` 统计相邻单元对中
  跨节点的比例（跨节点通信指标）。
- 路由器不启动任何后台 goroutine，`sync.RWMutex` 保护节点表，
  并发安全且无泄漏。

## 运行样例

```bash
export GO111MODULE=on
go run ./cmd/demo
```

输出：

```
GPS (39.9042, 116.4074) @ res=8 -> cell 0c120000ab000020
cell center: (39.785296, 116.317652), approx area: 872.361 km^2
k=3 disk: 37 cells (closed, hole-free), outer ring: 18 cells
disk coverage area: 32273.89 km^2
routed 37 cells via 4 virtual shard(s) to 3 node(s):
  node-a: 13 cells
  node-b: 0 cells
  node-c: 23 cells
  node-d: 1 cells
  node-e: 0 cells
  node-f: 0 cells
  node-g: 0 cells
  node-h: 0 cells
adjacent pairs: 90, cross-node pairs: 23 (25.6%)
```

验收点：GPS `(39.9042, 116.4074)` 在 Resolution=8 编码为十六进制
`0c120000ab000020`；半径 3 环共 37 个邻居单元（盘 `1+3·3·4=37`，
外环 `6·3=18`），覆盖连续闭合、无空洞。

## 测试结果

```bash
export GO111MODULE=on
go test ./...          # 功能与边界条件
go test -race ./...    # 并发安全性（竞态检测）
```

```
ok  hexgsb/hexgrid   # 12 个测试
ok  hexgsb/shard     # 10 个测试
```

- `hexgrid`：金样编码、字符串往返、非法输入、分辨率越界 panic、
  父子一致性（含跨面）、K 环尺寸/唯一性/无空洞/封闭性、跨面邻接对称、
  顶点附近五边形缺陷、拓扑距离、并发编码与遍历。
- `shard`：分片局部性（k=3 盘 ≤ 8 分片）、路由确定性、计划全覆盖、
  rendezvous 最小迁移（< 20%）、负载均衡、节点增删边界、空路由器、
  非法分片数 panic、局部性得分上限、并发读写 + goroutine 无泄漏断言。

## 目录结构

```
hexgrid/    二十面体几何、单元编码、层级缩放、K 环遍历
shard/      虚拟分片、rendezvous 哈希、局部性路由
cmd/demo/   运行样例
```
