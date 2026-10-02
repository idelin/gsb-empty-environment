# mapmatch — HMM 时空轨迹地图匹配内核

基于隐马尔可夫模型（HMM）+ Viterbi 解码的离散 GPS 轨迹路网匹配库，纯 Go 标准库实现。

## 模型

- **发射概率（Emission）**：GPS 测量误差服从零均值高斯分布，
  `p = N(0, σ)` 作用于观测点到候选路段投影的垂直距离（默认 `σ = 25 m`）。
- **转移概率（Transition）**：
  - 拓扑连通性：相邻两个候选点之间沿路网的最短路距离（Dijkstra，有向边）
    与直线距离之差服从指数分布 `p = (1/β)·exp(-|d_route - d_linear|/β)`（默认 `β = 30 m`）；
  - 速度合理性：隐含车速超过 `MaxSpeed`（默认 45 m/s）的转移被禁止；
  - 不连通（最短路径为 ∞）的转移概率为 0。
- **解码**：Viterbi 算法在候选路段图上求全局最可能路径；
  某时刻所有候选均不可达时自动以发射概率重启链，保证后续点正常解码。

## 结构

- `geometry.go` — WGS84 坐标、Haversine 距离、局部平面投影
- `graph.go` — 有向路网图、Dijkstra 最短路、候选点间路网距离
- `matcher.go` — HMM 匹配器：候选搜索、发射/转移概率、Viterbi 解码
- `matcher_test.go` — 单元测试 + 城市峡谷多径漂移端到端验收
- `cmd/demo/main.go` — 可运行样例

## 运行

```sh
go test -v ./...   # 测试
go run ./cmd/demo  # 样例：城市峡谷漂移轨迹贴合回道路中心线
```

## 用法

```go
g := mapmatch.NewGraph(nodes) // nodes: []mapmatch.Point（路口坐标）
g.AddUndirected(0, 1)         // 双向街道；AddEdge 加单向边

m := mapmatch.NewMatcher(g)   // σ/β/MaxSpeed/候选半径均可调
matches := m.Match(trace)     // trace: []mapmatch.GPSPoint（含时间戳）
// matches[i].Point 为贴合到道路中心线的坐标，EdgeID 为匹配路段
```

## 验收场景（见测试与样例输出）

4×4 城市网格（100 m 街区）上，车辆以 8 m/s 行驶，GPS 叠加 σ=12 m 噪声，
并每 9 个点注入一次 50–80 m 的多径效应漂移。Viterbi 全局解码后：

- 平均误差从 18.9 m 降至 9.8 m（已贴近沿轨迹方向噪声的理论下界 ≈9.6 m）；
- 83.6 m / 76.0 m / 50.8 m 的剧烈漂移点分别被纠正到 1.3 m / 5.2 m / 18.6 m；
- 90%+ 的匹配点落在真实位置 20 m 以内，散乱散点被平滑贴合至道路中心线。
