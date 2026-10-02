# trajsimp — 时态 Douglas-Peucker 轨迹简化器

仅用 Go 标准库实现的地理时空轨迹简化器。在经典 Ramer-Douglas-Peucker
递归（按垂直空间距离分裂折线）之上，为每个候选分裂点计算三项归一化代价，
取最大值超过 1 即分裂：

| 代价 | 含义 | 容差 |
| --- | --- | --- |
| 垂直距离 / `Epsilon` | 空间形状保真 | `Epsilon`（默认 5 m） |
| 时间同步距离（SED）/ `Epsilon` | 时间均匀性：按时间插值的同步位置偏差 | `Epsilon` |
| 相对速度误差 / `SpeedTolerance` | 速度平滑度：窗口化局部速度 vs 段隐含速度 | `SpeedTolerance`（默认 10%） |

此外，`DetectStays` 先检测停留区间（半径 `StayRadius`、最短时长 `MinStay`），
其端点被强制保留，保证停留等待与拐弯事件不失真。当段速度与局部速度都接近
零（停止状态）时速度偏差置 0，避免相对误差在静止时发散。

## 用法

```go
opt := trajsimp.Options{Epsilon: 5, SpeedTolerance: 0.10}
kept := trajsimp.SimplifyIndices(points, opt)   // 保留点的下标
simple := trajsimp.Simplify(points, opt)        // 精简后的轨迹
maxSync, maxSpeedDev := trajsimp.Validate(points, kept, opt) // 结果校验
```

## 运行样例

```
go run ./cmd/trajsimp-demo
```

输出（1 万采样点、含 2 段停留与 3 次转弯、±0.5 m GPS 噪声）：

```
输入采样点:        10001
精简后点数:        8 (剔除 99.92%)
耗时:              4.8ms
停留事件:          2 段（端点全部保留）
最大时间同步误差:  1.278 m (容差 5 m)
最大速度误差:      2.80% (容差 10%)
```

## 测试

```
go test ./...        # 单元测试（含 1 万点验收用例）
go test -bench=. .   # 性能：1 万点约 11 ms（Apple M1 Pro）
```

验收用例 `TestSimplifyLargeTrajectory` 断言：剔除率 ≥ 80%（实测 99.92%）、
全部停留区间端点保留、所有转弯点到精简折线的垂直距离 ≤ 5 m、
全轨迹最大时间同步误差 ≤ 5 m、最大速度误差 ≤ 10%。
