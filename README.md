# traj — 物流轨迹重建库

从有向边（重复边以边 id 区分）重建欧拉轨迹：从指定起点出发，每条边恰好走一次，
在所有合法路线中选出边 id 序列字典序最小的一条。

## 核心能力

- `FindRoute(edges, start)`：字典序最小欧拉路线。贪心逐位尝试最小边 id，
  每步做可行性检查（度数 + 连通性），保证不会把余边困死；无可行路线时返回
  `Diagnosis`，区分度数失败（`FailDegree`，附各点出入度差）与连通性失败
  （`FailConnectivity`，附从起点不可达的站点）。
- `BruteForceMinRoute` / `VerifyExhaustive`：≤10 边时全排列穷举，核对贪心结果
  确实是字典序最小合法路线（`ExhaustiveLimit = 10`）。
- `Graph.ImportBatch`：边分批到达，每批导入使边集版本 +1；边 id 全图唯一，
  批次原子拒绝重复。
- `Session`：业务给定不可改变的已走前缀（`WalkPrefix` 校验存在性、不重走、
  从当前点接续）；`RemainingRoute` 从当前点继续覆盖全部未走边，旧边绝不重走。
- `PublishRemaining` + `Graph.CheckRoute`：发布剩余路线时携带边集版本，
  导入新边后旧路线判为失效（`ErrStaleRoute`）。
- `Save` / `Restore`：断点恢复保存已走边身份与版本；恢复时校验版本一致并
  重放前缀；`VerifyRemainingExhaustive` 用全排列核对前缀约束下的剩余路线。

## 测试

```
go test ./...
```

含 500 组随机小图（≤8 边）与全排列穷举的对拍测试。
