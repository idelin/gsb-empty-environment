# trajsimp — 时态 Douglas-Peucker 轨迹简化器

带时间戳约束的 Ramer-Douglas-Peucker（RDP）轨迹线简化算法，纯 Go 标准库实现。

## 算法

在经典 RDP 按**垂直空间距离**递归分裂折线的基础上，对每个候选剔除点叠加两类时态判定：

1. **时间均匀性（SED）**：同步欧氏距离——采样点与简化线段上按时间线性插值出的"应有位置"之间的距离，必须 ≤ 空间容差。匀速直线段可以安全剔除，而加减速、徘徊会被保留。
2. **速度平滑度**：被剔除点的平滑速度（±3 采样窗口）与简化线段隐含速度的相对误差，必须 ≤ 速度容差。直路上的变速点因此不会被抹掉。

此外，算法前置检测并锚定两类关键事件，保证不失真：

- **停留时段**：平滑速度低于 `StopSpeed` 且持续 ≥ `MinStop` 的片段，其起止点被锚定，停留内部坍缩为两点；
- **拐弯点**：行进中航向突变超过 `TurnAngle` 的采样点被锚定。

分裂判据取三者归一化偏差的最大值（垂直距离/空间容差、SED/空间容差、速度误差/速度容差），> 1 即在最差点分裂，迭代至收敛。

## 使用

```go
import "trajsimp"

s := trajsimp.Default() // 5 m 空间容差, 10% 速度容差
out := s.Simplify(points)
maxSED, maxSpeedErr := s.Validate(points, out)
```

容差可通过 `trajsimp.Simplifier` 字段自定义（`SpatialTol`、`SpeedTol`、`StopSpeed`、`TurnAngle`、`Window`、`MinStop`）。

## 运行样例

```
go run ./cmd/demo
```

```
=== 时空轨迹 RDP 简化样例 ===
原始采样点:        10000
精简后采样点:      36
冗余剔除率:        99.6% (要求 >= 80%)
最大同步时空偏差:  4.982 m (容差 5.0 m)
最大速度误差:      8.78% (容差 10%)
停留事件保留:      2/2
拐弯事件保留:      3/3
耗时:              31.6ms
结果: PASS
```

## 测试

```
go test ./... -v
```

覆盖：直线段坍缩为 2 点、拐弯保留、停留保留、直路变速保留、1 万点验收用例
（剔除 ≥80%、SED ≤ 5 m、速度误差 ≤ 10%、2 个停留与 3 个拐弯全部保留）、
边界输入（空/单点/两点/重复时间戳）。

```
--- PASS: TestStraightLineReduction
--- PASS: TestTurnPreserved
--- PASS: TestStopPreserved
--- PASS: TestSpeedChangePreserved
--- PASS: TestAcceptance10k
    10k points -> 36 kept (99.6% removed) in 5.6ms
    max SED 4.982 m, max speed error 8.78%, stops 2/2, turns 3/3
--- PASS: TestEdgeCases
--- PASS: ExampleSimplify
PASS
ok  	trajsimp	0.822s
```
