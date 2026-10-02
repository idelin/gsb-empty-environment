# orgtemporal — 可追溯员工组织架构时态树

纯 Go 标准库实现的双时态（bitemporal）组织架构组件：部门归属与汇报线均携带
**业务生效时间段** 与 **系统知悉时间段**，支持历史任意时刻回溯与补录修订。

## 模型

每条任职记录 `Assignment` 包含四个时间坐标：

| 维度 | 字段 | 含义 |
| --- | --- | --- |
| 业务时间 | `ValidFrom` / `ValidTo` | 汇报关系在现实世界的生效区间，左闭右开 |
| 知悉时间 | `RecordedAt` / `SupersededAt` | 系统录入时刻 / 被后续修订取代的时刻 |

查询 `(知悉时刻 K, 业务时刻 T)` 命中的记录满足：
`RecordedAt <= K < SupersededAt` 且 `ValidFrom <= T < ValidTo`。

## 补录与自动分裂

`Store.Record` 录入与当前最新认知重叠的历史事件时：

1. 被重叠的旧记录整体标记 `SupersededAt = 当前知悉时刻`（不删除，保留历史）；
2. 旧记录未重叠的左/右残段以新知悉时刻重新落成记录；
3. 新事件接管重叠区间。

知悉时间必须单调递增，早于补录时刻的历史视图不受影响。

## API

- `Record(emp, mgr, dept, from, to, knowledge)` — 录入/补录任职记录（自动分裂重叠区间）
- `ManagerAt(emp, knowledge, business)` — 某知悉时刻看来，某业务时刻汇报给谁
- `DepartmentAt(emp, knowledge, business)` — 同上，查部门归属
- `Chain(emp, knowledge, business)` — 该时点向上的完整汇报链（带环检测）
- `Segments(emp, knowledge)` — 该知悉时刻的最新认知时间线分段

## 运行

```sh
go test ./... -v   # 测试（含验收场景）
go run ./cmd/demo  # 运行样例
```

## 验收场景

E 在业务 [1,10) 汇报 M1（知悉 1 录入）；知悉 5 补录业务 [3,7) 汇报 M2：

- 知悉 6 查询业务 4 → **M2**（补录生效）
- 知悉 6 查询业务 8 → **M1**（补录区间外）
- 知悉 4 查询业务 4 → **M1**（补录前的历史认知）

补录后 E 的最新认知时间线自动分裂为 `[1,3)→M1`、`[3,7)→M2`、`[7,10)→M1`。
