# mapmatch — HMM 时空轨迹地图匹配内核

基于隐马尔可夫模型（HMM）+ Viterbi 算法的离散 GPS 轨迹路网匹配库。
仅使用 Go 标准库。

## 算法

- **隐状态**：路网候选路段上的投影点（候选由 `CandidateRadius` 内的路段生成）。
- **发射概率（Emission）**：GPS 测量误差服从高斯分布 `N(0, Sigma)`，
  取观测点到候选路段中心线的垂直距离计算。
- **转移概率（Transition）**：沿路网拓扑的最短路（Dijkstra）行驶距离与
  相邻两 GPS 点的直线距离之差服从指数分布（Newson & Krumm, 2009），
  并叠加行驶速度合理性惩罚（隐含车速超过 `MaxSpeed` 时按比值衰减）。
- **解码**：Viterbi 算法在候选路段图上求全局最可能路径，输出贴合道路
  中心线的匹配点序列。无候选的离群观测点自动跳过。

## 参数

| 参数 | 含义 | 默认值 |
|---|---|---|
| `Sigma` | GPS 测量误差标准差（米） | 15 |
| `Beta` | 转移概率指数分布尺度（米） | 30 |
| `MaxSpeed` | 最大合理车速（米/秒） | 40 |
| `CandidateRadius` | 候选路段搜索半径（米） | 100 |

城市峡谷等多径效应严重的场景建议增大 `Sigma`（如 25）、减小 `Beta`（如 20），
让拓扑一致性主导，纠正漂移点。

## 运行样例

```sh
go run ./cmd/demo
```

样例构造"城市峡谷"路网：主路（y=0）与 60 米外的平行路，三条横街相连。
模拟车辆以 10 m/s 沿主路行驶，GPS 每 5 秒一个点，部分点因多径效应
向平行路方向漂移约 55 米。输出（节选）：

```
#    t(s)     raw GPS (lat,lon)     matched (lat,lon)     edge     rawErr(m) matchErr(m)
6    30       (31.230837, 121.477460) (31.230400, 121.477460) Main   49.2      7.5
7    35       (31.230958, 121.477875) (31.230400, 121.477875) Main   62.1      3.1
8    40       (31.230936, 121.478396) (31.230400, 121.478396) Main   59.7      3.5
...
mean error: raw GPS 17.9 m  ->  matched 5.0 m
```

漂移 49–62 米的散点被全部纠正回主路（`Main`）中心线上。

## 测试

```sh
go test -v ./...
```

```
=== RUN   TestMatchCityCanyon   # 验收：剧烈漂移点全部匹配回真实道路
--- PASS: TestMatchCityCanyon (0.00s)
    raw GPS mean error:  17.89 m
    matched mean error:  4.98 m
=== RUN   TestMatchOutlierSkipped  # 无路网候选的离群点被跳过
--- PASS: TestMatchOutlierSkipped (0.00s)
=== RUN   TestProjectPoint      # 点到线段投影
--- PASS: TestProjectPoint (0.00s)
=== RUN   TestDijkstra          # 路网最短路
--- PASS: TestDijkstra (0.00s)
=== RUN   TestRouteDistance     # 候选点间路网行驶距离
--- PASS: TestRouteDistance (0.00s)
PASS
ok      mapmatch
```

## 用法

```go
g := mapmatch.NewGraph(nodes)              // nodes: []mapmatch.Point
g.AddEdge("MainSt", 0, 1, centerline)      // centerline: 折线 []Point

m := mapmatch.NewMatcher(g)
m.Sigma, m.Beta, m.MaxSpeed = 25, 20, 30
results := m.Match(gpsPoints, timestamps)  // timestamps 单位：秒
for _, r := range results {
    // r.Cand.Point 为贴合道路中心线的匹配点，r.Cand.Edge 为匹配路段
}
```

## 文件结构

- `geo.go` — 经纬度/局部平面坐标换算、haversine、点到折线投影
- `graph.go` — 路网图结构与 Dijkstra 最短路
- `match.go` — HMM（发射/转移概率）与 Viterbi 解码
- `match_test.go` — 单元测试与城市峡谷验收测试
- `cmd/demo/main.go` — 可运行样例
