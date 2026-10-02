# hexgsb — 离散六边形分层空间索引 + 多节点并发冲突仲裁器 (Go)

纯标准库实现，无第三方依赖。包含两部分：

1. **`hexgrid`** — 基于二十面体展开的离散六边形地理网格：
   - 20 个三角面配对为 10 个菱形（rhombus）区域，每个菱形承载 `n×n`（`n = 2^res`）
     的六边形轴坐标 `(q, r)` 晶格；
   - 经纬度 → 分层网格编码（64 位 `CellID`，10 位十六进制字符串）；
   - 父子单元缩放（每级 1→4 分裂，`Parent` / `Children` / `ParentAt`）；
   - **纯整数**拓扑 K-Ring 邻域遍历：初始化时一次性构建菱形边过渡表
     （目标菱形 / 目标边 / 方向翻转），遍历全程以半格整数坐标 `(2q+1, 2r+1)`
     做跨界映射，无任何浮点三角计算。
2. **`arbiter`** — 多执行体并发竞争与冲突仲裁器：
   - 多单元**原子**获取（全有或全无）、单互斥锁保证**线性一致性**；
   - 等待时不持锁（代际广播 channel + 重试），**无死锁**；
   - 每单元单调递增 **fencing token**，过期授权无法释放已转授的单元；
   - 超时 / 取消的等待者不留任何注册状态，**无 goroutine 泄漏**。

## 布局

```
hexgrid/icosahedron.go  二十面体顶点/面构建、菱形配对、边过渡表
hexgrid/cell.go         CellID 位布局、十六进制编解码、父子层级
hexgrid/geo.go          经纬度 <-> 单元编码（仅编码端使用浮点）
hexgrid/kring.go        纯整数邻居 / Ring / K-Ring 遍历
arbiter/arbiter.go      并发仲裁器（Acquire/TryAcquire/Release/Fence）
cmd/demo/main.go        端到端运行样例
```

## 运行样例

```sh
GO111MODULE=on go run ./cmd/demo
```

输出（节选）：

```
GPS (39.9042, 116.4074) @ res 8 -> cell 2080558020
  rhombus=2 q=171 r=32 center=(39.8938, 116.4359)
  parent(res 7) = 1c802a8010
  parent children: 2080550020 2080558020 2080550021 2080558021
K-Ring k=3: 37 cells (expected 37), contiguous closed disk
  ring[ 0] 2080558020
  ring[ 1] 2080550021
  ...
arbiter: 0 live grants after run (expect 0)
  node-0 won 20 exclusive windows
  ...
  node-7 won 20 exclusive windows
```

验收边界：GPS `(39.9042, 116.4074)` 编码为 Resolution=8 的十六进制串
`2080558020`；其半径 3 环邻域共 `1 + 3·3·4 = 37` 个单元，测试通过 BFS
连通性 + 逐层闭合性断言面积覆盖连续、无空洞遗漏。

## 测试

```sh
GO111MODULE=on go test -race -count=1 ./...
```

结果（Go 1.11.4 darwin/amd64，`-race` 全部通过）：

```
--- PASS: TestEncodeGolden          黄金值：res-8 编码 = 2080558020
--- PASS: TestParseRoundTrip        十六进制编解码往返
--- PASS: TestHierarchy             父子缩放全层级一致性
--- PASS: TestRootCells             res-0 恰好覆盖 10 个菱形
--- PASS: TestKRingAcceptance       37 单元、环尺寸 6r、BFS 连通无空洞
--- PASS: TestKRingRhombusCrossing  跨菱形/跨角邻域闭合、邻接对称
--- PASS: TestCenterRoundTrip       encode(center(c)) == c
--- PASS: TestVertexDefect          顶点缺陷区（5 面交汇）去重行为
--- PASS: TestConcurrentEncode      16 goroutine 并发编码（-race）
ok  hexgsb/hexgrid

--- PASS: TestMutualExclusion       16×300 竞争下单计数器互斥（-race）
--- PASS: TestLinearizability       所有权区间两两不重叠
--- PASS: TestAllOrNothing          多单元原子获取，失败零残留
--- PASS: TestTimeoutAndCancel      超时 / ctx 取消 / 空集合
--- PASS: TestStaleRelease          过期授权释放为幂等空操作
--- PASS: TestFencingMonotonic      fencing token 严格递增
--- PASS: TestDedupAndSort          请求集合去重排序
--- PASS: TestDeadlockFree          24 goroutine 随机多单元竞争全部终止
--- PASS: TestNoGoroutineLeak       超时/取消等待者零泄漏
--- PASS: TestGrantAccessors        授权句柄只读语义
ok  hexgsb/arbiter
```

## 设计说明

- **顶点缺陷**：二十面体 12 个顶点处只有 5 个三角面交汇（类比 H3 的
  五边形缺陷），其附近磁盘去重后单元数小于 `1+3k(k+1)`，属拓扑固有行为，
  由 `TestVertexDefect` 固定语义。
- **公平性**：仲裁器采用广播-重试而非 FIFO 队列；线性化点为互斥锁下的
  授权瞬间。fencing token 供下游存储识别过期写。
