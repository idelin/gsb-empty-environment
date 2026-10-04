# readrepair

Dynamo 风格读修复模拟器：协调者从多副本读取，按向量时钟归并出胜出版本，
并异步条件修复落后副本。

## 设计

- `vclock.go` — 向量时钟：`Compare` 返回 领先(-1) / 落后(+1) / 相等(0) / 并发(2)。
- `version.go` — `mergeVersions` 是核心归并：丢弃被严格支配的版本，保留全部
  并发分支（siblings）；同时钟去重时优先墓碑，删除永不丢失。
- `node.go` — 副本节点。`Put`（客户端写）与 `Repair`（读修复）走同一个条件归并，
  因此过期修复无法覆盖更新或并发的状态，旧值也无法复活墓碑。`Latency` 模拟
  网络延迟，`HookRepair` 供测试编排确定性交错。
- `coordinator.go` — `Read` 并发查询全部副本，收满 R 个响应即归并返回；
  迟到响应在后台继续归并进最终版本，再对落后副本发起条件修复。

## 不变式（由测试验证）

- 并发分支在返回值与修复值中都不丢失（`TestConcurrentBranchesSurviveReadAndRepair`）。
- 墓碑支配旧值，迟到的旧写无法复活已删 key（`TestTombstoneNotOverwrittenByOldValue`）。
- 修复在途时目标被并发更新，条件修复做归并而非覆盖
  （`TestRepairMergesWithConcurrentTargetUpdate`）。
- 慢副本的迟到响应仍被并入修复负载，最终全副本收敛到所有分支
  （`TestLateResponseMergedIntoRepair`）。

## 运行

```sh
go test -race ./...
```
