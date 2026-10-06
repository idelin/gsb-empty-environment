# trail — 物流轨迹重建库

由有向边（每条边有唯一 id，允许平行边）重建物流轨迹：从指定起点出发，
每条边恰好走一次，在所有合法路线中选出边 id 序列字典序最小的一条。

## 核心求解：`Solve(start, edges)`

- 返回字典序最小的边 id 序列；贪心（带桥检查的有向 Fleury）每步选
  不会困住余边的最小 id 边。
- 边数 ≤ 10 时用全排列穷举交叉核对贪心结果，防止贪心选择导致余边被困。
- 不可解时返回 `*Failure`：
  - `FailureDegree`：入/出度不平衡（附各失衡节点的 in/out）。
  - `FailureConnectivity`：部分边从起点不可达（附不可达边 id 列表）。

## 增量规划：`Planner`

边分批到达，业务侧给出不可改变的已走前缀：

- `NewPlanner(start, edges, walked)`：校验前缀是从起点出发的链、边 id
  均存在且不重复；余图路线从当前点（前缀末端）继续，旧边不重走。
- `PublishRemaining()`：发布覆盖全部未走边的剩余路线，附当前边集合
  版本号（`Plan.Version`）。
- `ImportBatch(batch)`：导入新边并递增版本，此前发布的旧路线随之失效
  （`ValidatePlan` 返回 `ErrStalePlan`）。
- `Checkpoint()` / `Recover(start, edges, cp)`：断点恢复。检查点保存
  已走边身份与版本；恢复时重校验前缀，并在小规模（≤10 边）下用
  全排列核对前缀限制——确认已走前缀之后仍存在可行完成路线。

## 测试

```sh
GO111MODULE=on go test ./trail/
```

含随机小图对拍（贪心 vs 穷举）、贪心陷阱用例、度数/连通性失败、
前缀校验、版本失效与断点恢复等 18 个用例。
