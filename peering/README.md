# peering — Ceph 风格 PG Peering 控制面包

`prescreen.local/ps-df72a8096a/peering` 是一个零依赖、可直接导入的 Go 包，
实现分布式对象存储 Placement Group 在 OSDMap 频繁变动、节点反复上下线与
网络分区下的 Peering 协商、权威日志合并与恢复状态机。包不会在不安全时
私自 `Active`：任何阻断性历史缺口、无可达副本或 `min_size` 不足都会停在
`Incomplete` 并给出精确原因。

## 四大核心算法与 API 映射

1. **`past_intervals` 与 `maybe_went_rw` 权威历史回溯**
   - `BuildPastIntervals(epochs)`：将连续 `up/acting/min_size` 相同的纪元合并为
     区间；仅当区间曾 `active` 且 `len(acting) >= min_size` 时置 `MaybeWentRW=true`。
   - `FindUnwitnessedInterval(intervals, queried)`：每个可写区间都必须有至少一个
     成员参与本次日志探测，否则返回该区间，状态机据此阻塞为 `Incomplete`
     （`ReasonHistoryGap`），绝不在全员失联时激活。
2. **权威日志选择与分叉日志回滚**
   - `SelectAuthLog(replicas)`：先按最大 `LastEpochStarted` 过滤（真实完成过
     Peering 的激活），再用 `LastComplete`、`LastUpdate` 与 OSDID 决胜，因此不会
     盲信 `last_update` 最高的孤立旧主。
   - `MergeLogs(replicas)`：逐条目识别 `divergent_priors`
     （`DivergentAfterLES` / `DivergentFork` / `DivergentNotAuth`），对孤立旧主
     在分区中产生、未达 `min_size` 的条目截断回滚，并输出每 OSD 的
     `RolledBackLog` 与权威最新对象版本 `AuthLatest`。
3. **`up` vs `acting`、`pg_temp` 与 `missing` 推导**
   - `PlanRecovery(up, alive, merge)`：将无法增量追平的新成员（标记 backfill、
     空副本或日志尾部过旧）放入 `Backfill`，并用完整副本构造 `PgTemp` 作为
     `Acting`，提前恢复 `active+degraded`；同时为每个成员计算
     `MissingEntry` 集合与全局 `MissingLocations` 恢复源。
4. **`unfound` 检测与 `min_size` 写门禁**
   - 权威日志声明的最新版本在所有在线 OSD 上都不存在时记入 `Unfound`，
     `Result.ObjectIOAllowed(object)` 对该对象单独禁读写。
   - 仅当可用完整副本数 `>= min_size` 且无阻断性历史缺口时，`PG.Peer()` 才转入
     `Active` 并置 `CanWrite=true`；否则停在 `Incomplete`（`ReasonMinSize` 等）。

## 顶层入口

```go
pg := peering.NewPG("pg.0", minSize, currentUp, historyEpochs, replicas...)
result := pg.Peer()

switch result.State {
case peering.StateActive:
    // result.CanWrite 受 min_size 门禁；单对象用 result.ObjectIOAllowed(obj)
case peering.StateIncomplete:
    // result.BlockedReason / result.Phase 给出精确阻断点
}
```

`Result` 同时暴露 `Intervals`、`Unwitnessed`、`Merge`（含 `Divergent` 与
`RolledBack`）、`Recovery`（含 `PgTemp`、`Missing`、`MissingLocations`、
`Unfound`）等中间产物，便于控制面观测与测试断言。

## 验收测试

`peering_test.go` 覆盖：

- `TestHistoryGapBlocksIncomplete`：历史 `maybe_went_rw` 区间全员失联 → `Incomplete`；
- `TestDivergentLogRollback` / `TestDivergentClassification`：分区孤立旧主高分叉
  `last_update` 不被信任，孤立条目精准截断回滚，含同版本分叉与未知版本分类；
- `TestPgTempAndMissing`：`up != acting` 生成 `pg_temp`，逐成员精确 `missing` 与
  `missing_locations`；
- `TestUnfoundAndMinSizeGate`：`unfound` 对象定位与单对象禁读写，以及跌破
  `min_size` 时阻塞 `Incomplete`。

运行：

```sh
go test ./...
go test -race ./...
```
