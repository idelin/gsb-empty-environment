# hexgrid — 分区存储的轴坐标六边形网格可达性查询

平面轴坐标 `(q, r)` 六边形网格，六个邻居偏移固定为
`(1,0) (1,-1) (0,-1) (-1,0) (-1,1) (0,1)`（见 `hex.go` 的 `Directions`）。
纯二维网格，不涉及经纬度或地球投影。

## 模型

- **有限地图**：输入为可通行单元 + 障碍单元，其余坐标视为地图外（不可通行）。
- **分区只是存储边界**：`Layout{PageSize, PartitionSize}` 把平面切成页、把页切成
  分区（`Layout.Locate`，对负坐标用向下取整除法）。邻接关系只由 `Directions`
  决定，与分区无关。
- **版本化（MVCC）**：`Store.Update(changes)` 原子地批量修改通行性并把版本 +1，
  每页保留历史快照，旧版本查询（`Query.Version`）照常可用；
  `DropHistoryBefore` 回收旧快照后，对应版本查询返回 `ErrVersionGone`。
- **异步按页加载 / 版本不一致**：查询固定一个版本。取页时缺页
  （`ErrMissingPage`）或分区版本滞后（`ErrStaleVersion`）会重试至多
  `MaxRetries` 次，仍失败则**整个查询拒绝**，绝不把不同版本拼成不存在的地图。
  测试用 `DeferPageLoad` / `DesyncPartition` 模拟异步加载与分区滞后。

## 查询语义

`Store.Query(Query{Start, K, Target, Version})` 返回：

- `Reachable`：K 步内可达单元（按 `(q,r)` 字典序排序）；
- `Dist`：每个可达单元的最短步数；
- `Parent` / `PathTo` / `Path`：最短路径中按坐标字典序决胜的那一条
  （分层 BFS + 层内秩比较实现，见 `query.go` 的 `runQuery`）；
- `Stats`：本次查询访问量（取页次数、重试次数、BFS 扩展次数）。

边界情况：`K=0` 只返回可通行的起点；起点是障碍或在地图外返回
`ErrStartBlocked`；`Target` 不可达返回 `ErrTargetUnreachable`（附带部分结果）。

## 正确性验证（不靠"画面没有空洞"）

`Reference` 是不分区的平铺实现，与 `Store` 共用同一个 BFS 核心；测试在
跨多分区、含负坐标、随机障碍、批量更新前后的场景下逐查询对拍
（可达集合、距离、路径完全一致）。

```
go test ./...                          # 全部测试
go run ./cmd/hexreplay testdata/replay.json   # 重放 JSON 例子，输出报告
```

`testdata/replay.json` 是可重放例子：同一张图先发版（v1），再批量封锁两格
（v2），查询覆盖旧版本查询、字典序决胜路径、K=0、障碍/图外起点报错；
每条查询都与参考实现和钉死的期望值比对，并输出访问量统计。
