# pdp — 容量约束取送路径库

单车辆、起终点相同的取送（pickup-and-delivery）精确求解库，最多 6 对请求。
距离矩阵完整且非负；取货先于送货，送达释放容量。

## 静态求解

- `Solve(p Problem) (Route, error)`：子集 DP 求联合最短路线（不按请求拆成
  独立最短路）；费用并列时按访问点 id 序列字典序取最小。
- `Route.Stops` 携带每站之后的载重，即载重轨迹（`Route.Loads()`）；
  `Route.TotalDist` 含返回车场末段。
- `SolveBrute`：合法排列全枚举的参考实现，用于验证 `Solve`。

## 在线取消

`Simulator` 按事件流驱动；已完成前缀固定，每次事件后从当前位置与载重
重算最优剩余路线，并返回 `Snapshot`（每件状态、位置、载重、前缀与剩余
路线的载重证据）。

- 事件按提交顺序生效，由此确定"同刻"语义：取消先于送达步骤提交 → 货物
  仍在车上，必须送到指定退货点且继续占容量；送达步骤先执行 → 取消被拒绝。
- 未取货取消：请求整体移除；已取货取消：生成到退货点的 `ReturnStop`。
- `CrossCheck()` 用剩余事件的合法排列全枚举核对当前剩余计划。

```go
sim, _ := pdp.NewSimulator(prob)
snap, _ := sim.Apply(pdp.Step())          // 执行下一计划站
snap, _ = sim.Apply(pdp.Cancel(3, 7))     // 取消请求 3，退货点 7
```
