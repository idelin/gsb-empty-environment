# hexgsb — 离散六边形分层空间索引 + 多节点并发冲突仲裁器（Go）

基于二十面体展开的离散六边形地理网格系统（H3 风格 DGGS），以及构建在其上的
多执行体并发竞争与冲突仲裁器。仅使用 Go 标准库。

## 设计要点

### 1. 二十面体离散六边形网格（`hexgrid`）

- **几何基底**：正二十面体（12 顶点 / 20 面 / 30 边）。经纬度方向向量先按
  面法向最大点积选面，再经**球心投影（gnomonic）**投到面平面上，解出重心坐标。
  浮点与三角计算只出现在这条编码路径上。
- **格点模型**：每个面上是频率 `N = 7^res` 的三角格。格点 `(i, j)` 即 Eisenstein
  整数 `i + jω`（`ω = e^{iπ/3}`）。孔径 7 层级由乘以 `(2+ω)`（范数 7）生成：
  - 父单元：`round((2+ω)·c / 7)`（整数 cube-round，分母 7）
  - 子单元：`c' = (2+ω̄)·c + u_d`，`u_d ∈ {0, ±1, ±ω, ±ω²}`（数字 0..6）
  - 由于 `M = 2+ω` 与 60° 旋转可交换，**跨面展开变换（旋转 60° 的倍数 + 平移）
    精确保持子格**，因此层级在全球范围严格一致，无浮点、无三角。
- **跨面拓扑**：20×3 条边的整数变换表（重心坐标 `(w0,w1,w2)` 上的 ±1 矩阵），
  由面邻接在 `init` 时自动构建并校验。K-Ring 遍历 = 单元空间整数偏移 →
  面格点 → 跨面归一化 → 规范化（共享边/顶点归属最小面编号）→ 编码。
  **全程纯整数运算**。
- **五边形**：12 个二十面体顶点处天然形成五边形单元（每分辨率恰好 12 个），
  其邻域少一个扇区，遍历自动去重。
- **单元编码**：64 位，H3 风格布局：`mode(4) | res(4) | face(5) | slot(2) | 15×3bit digits`，
  输出为十六进制字符串。

### 2. 多节点并发竞争与冲突仲裁器（`arbiter`）

- **原子性**：多单元 `Acquire` / `Release` / `Renew` 全有或全无；冲突返回
  `ConflictError`（含逐单元持有者），不产生任何部分状态。
- **线性一致性**：64 个分片锁，所有操作按**分片编号升序**加锁（全局一致的锁序），
  重叠单元上的操作在公共分片上串行化；`Snapshot` 以同样锁序读取，得到一致快照。
- **无死锁**：锁序全局一致 ⇒ 不存在锁序环；临界区内无阻塞调用。
- **无竞态**：所有共享状态只在分片锁内访问；fencing token 由原子计数器单调发放，
  过期 token 被拒绝（`ErrStaleFence`）。
- **无泄漏**：不启动任何后台 goroutine；租约过期为惰性求值（访问时判定）。

## 目录结构

```
hexgrid/    二十面体网格：编码、层级、K-Ring 拓扑
arbiter/    多节点并发竞争与冲突仲裁器
cmd/demo/   运行样例
```

## 运行样例

```
$ go run ./cmd/demo
GPS (39.9042, 116.4074) @ res=8 -> cell 8805442a65fffff
cell center: (39.909109, 116.402557)
k=3 disk: 37 cells (per ring: map[0:1 1:6 2:12 3:18]), ring(3): 18 cells
parent@5: 8505442bfffffff, children@8: 343, contains self: true
concurrent claim of 37 cells by 8 nodes: winners=1, ownership=map[node-7:37]
```

验收边界：GPS `(39.9042, 116.4074)` 编码为 Resolution=8 的十六进制串
`8805442a65fffff`；半径 3 环共 **37** 个邻居（环分布 1/6/12/18），
`TestGridDiskAcceptance` 断言其连续、闭合、无空洞（每个距离 d 的单元都有
距离 d−1 的邻居；边界环每个单元都有盘外邻居；与 BFS 距离逐一吻合）。

## 测试结果

```
$ go test -race -count=1 ./...
ok  	hexgsb/arbiter	1.893s
ok  	hexgsb/hexgrid	5.963s
```

### hexgrid（11 项）

| 测试 | 覆盖点 |
| --- | --- |
| `TestIcosahedronTopology` | 30 边双向邻接、跨面往返恒等、跨面保持 `(2+ω)^r` 子格 |
| `TestEncodeCenterRoundTrip` | 2 万随机点 × res 0..12：编码→中心→再编码严格相等 |
| `TestHexStringRoundTrip` | 十六进制串解析往返、非法输入拒绝 |
| `TestBaseCellsAndPentagons` | 12 个 res-0 五边形、五边形 disk(1)=6 |
| `TestHierarchyParentChild` | 父/子互逆、祖先链结合律、孩子枚举包含自身 |
| `TestChildrenCountAndUniqueness` | 非五边形 7² 子单元唯一且父指针正确 |
| `TestChildrenPartition` | 全球无空洞：12 基单元的孩子恰好划分全球，res-2 恰好 492=10·7²+2，10 万随机点全覆盖 |
| `TestGridDiskAcceptance` | **验收边界**：res=8、k=3 → 37 单元、连续闭合无空洞、BFS 距离一致 |
| `TestGridDiskRandomCenters` | 500 随机中心 disk 计数 1+3k(k+1)、无重复 |
| `TestGridDiskCrossesFaces` | 跨面邻域无空洞（disk(2)=19） |
| `TestConcurrentReadOnlySafety` | 16 goroutine 并发编码/遍历（-race） |

### arbiter（10 项）

| 测试 | 覆盖点 |
| --- | --- |
| `TestAcquireReleaseBasic` | 基本声明/释放/快照 |
| `TestEmptyAndDuplicateAndTooMany` | 空集、重复单元去重、超限拒绝 |
| `TestConflictAtomicity` | 冲突全有或全无、持有者报告、自续租 |
| `TestConcurrentSingleCell` | 32 节点竞争同一单元，恰好 1 个赢家 |
| `TestReleaseValidation` | 非属主释放、双重释放、未持有释放、错误 fencing |
| `TestFencingMonotonicAndStale` | fencing 严格递增、续租轮换、旧 token 拒绝 |
| `TestLeaseExpiry` | 注入时钟：租约到期被接管、原属主被驱逐 |
| `TestLinearizableOwnership` | 8 节点 × 500 轮：所有权区间互不重叠（线性一致见证） |
| `TestConcurrentOverlappingClaims` | 16 节点 × 100 轮重叠多单元声明：无死锁（30s 看门狗）、原子性见证、结束后零残留 |
| `TestNoGoroutineLeak` | 压测后 goroutine 数回归基线，严格无泄漏 |
