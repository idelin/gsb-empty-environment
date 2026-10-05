# schedcheck — 资质感知的排班校验库

判断员工班次是否被有效证书完整覆盖，并追踪证书/吊销事件影响到的历史班次。

## 模型

- 时间一律为整数分钟，区间为左闭右开 `[start, end)`；跨午夜班次的 `end` 直接大于 1440。
- 岗位（`Position`）声明所需资质；一项资质可由同一员工的多张证书**无缝接续**覆盖（交界相等不构成缺口），不同资质分别检查。
- 事件（`Event`）是只追加日志：`issue`（发证）与 `revoke`（吊销，按区间从证书有效性中扣除，多条吊销取并集）。
- 每个事件携带**业务有效区间** `[start, end)` 与**录入序号** `seq`。序号为 `S` 的查询只能看到 `seq < S` 的事件，因此追溯补录的证书/吊销不会改变旧查询的答案。

## API（`sched` 包）

- `NewEngine(positions, shifts)`：构建引擎与按员工分桶、按开始分钟排序的班次区间索引。
- `Check(shiftID, asOfSeq)` / `CheckAll(asOfSeq)`：返回每个班次的可排班判断，以及每项缺失资质的**最小不相交缺口区间**。
- `AddEvent(ev)`：录入事件，只通过区间索引复核相交候选班次，返回判定发生变化的历史班次 ID。
- `AffectedByFullScan(ev)`：不使用索引、全量复核的参考实现（不修改引擎），用于对照验证。
- `AggregateByQual(reports)`：按资质聚合缺口及涉及的班次 ID。

## 命令行

```sh
go run ./cmd/schedcheck examples/scenario.json   # 或从 stdin 读入
```

输入 JSON 含 `positions` / `shifts` / `events` / `queries`；输出每个事件影响到的班次（索引法与全扫描法对照）以及每个查询的班次报告与按资质聚合的缺口。

## 测试

```sh
go test ./...
```

覆盖：跨午夜班、两证无缝接续、重叠吊销、追溯补证后旧查询不变、证书班次中途到期、一人多岗位、索引法与全扫描法逐事件对拍。
