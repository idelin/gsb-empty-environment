# hexgrid

离散六边形分层空间索引 + 自适应动态阈值伸缩与负载感知（Go，仅标准库）。

## 组成

### 1. 离散六边形分层地理网格（`cell.go` / `cube.go` / `geo.go` / `icosa.go` / `kring.go`）

基于二十面体展开的离散六边形地理网格系统（类 H3 的 64 位编码）：

- **经纬度 → 分层网格编码**：lat/lng → 单位球 → 最近二十面体面（base cell）→
  球心投影（gnomonic）到面平面 → 六边形晶格量化 → 7 进制分层数字链。
- **64 位 Cell 编码**：`[mode:4][reserved:4][resolution:4][baseCell:7][15×3bit digits]`，
  十六进制字符串表示（`cell.String()` / `ParseCell`）。
- **父子缩放**：`Parent(res)` / `Child(digit)` / `Children()`，每个单元 7 个子单元
  （数字 0 = 居中子单元，1..6 = 六个方向），分辨率每级晶格尺度减半。
- **拓扑 K-Ring 遍历**：`KRing(cell, k)` 在整数立方体坐标（cube coord, x+y+z=0）上
  做纯整数 BFS，逐步仅使用 6 个单位方向向量，**无浮点三角计算**；仅当邻居跨越
  二十面体边界时才经球面路径重编码。半径 k 的环盘恰含 `1+3k(k+1)` 个单元，
  连续闭合无空洞。
- `GridDistance(a, b)` 返回同面同分辨率单元间的拓扑距离。

### 2. 自适应动态阈值伸缩与负载感知（`adaptive.go`）

`AdaptiveController` 根据外部压力与环境指标（CPU/内存/队列深度/错误率的加权
复合压力）自调节内部阈值（准入限额/并发预算等）：

- **EMA 平滑** 抑制噪声毛刺；
- **迟滞带**（高/低水位之间不动作）避免抖动；
- **AIMD 伸缩**：过载乘法退避、空闲加法恢复；
- **负载感知增益** `PressureGain`：调节幅度随越限压力比例放大；
- **冷却窗口** `Cooldown`：两次调节间的最小间隔，规避雪崩/踩踏式反应；
- **硬边界** `[Min, Max]` 钳制，严格不漂移；
- `Allow(cost)` 提供负载感知准入控制。
- 控制器不启动任何 goroutine，全部 API 并发安全（`sync.Mutex`）。

## 使用

```go
cell := hexgrid.GeoToCell(39.9042, 116.4074, 8) // GPS -> res 8
fmt.Println(cell)                               // 1080663b00bfffff
ring := hexgrid.KRing(cell, 3)                  // 37 个邻居单元（含自身）

ctrl, _ := hexgrid.NewAdaptiveController(hexgrid.AdaptiveConfig{
    Min: 10, Max: 1000, Initial: 500,
    HighWatermark: 0.75, LowWatermark: 0.30,
    IncreaseStep: 25, DecreaseFactor: 0.7,
    Smoothing: 0.5, Cooldown: 100 * time.Millisecond,
    PressureGain: 1.0,
})
adj := ctrl.Observe(hexgrid.EnvMetrics{CPUUtil: 0.9, QueueDepth: 0.8})
ok := ctrl.Allow(cost)
```

## 运行

```sh
go test -race ./...   # 全部测试（含竞态检测）
go run ./cmd/demo     # 端到端示例
```
