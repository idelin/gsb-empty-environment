# trijoin

可导入的 Go 三路区间连接增量维护包。模块路径：

```go
import "prescreen.local/ps-c9e5d9e25f"
```

包名为 `trijoin`。

## 语义

- 三流共享 `Key`。
- A 与 B：`t_B ∈ [t_A-d1, t_A+d2]`。
- B 与 C：`t_C ∈ [t_B-d3, t_B+d4]`。
- 输出三元组为 A×B×C，其行号是 `OutputID{ARow, BRow, CRow}`。
- `Weight=+1` 插入，`Weight=-1` 撤回；负数可以先于对应插入到达。负权重会作为墓碑保留，直到后续插入抵消。
- 一次 `ApplyBatch` 内的所有输出增量按 `(Key, OutputID)` 合并；净权重为零的三元组不发射。

## 快速使用

```go
j := trijoin.New(2, 3, 1, 2) // d1, d2, d3, d4

deltas := j.Apply(
    trijoin.RecordEvent(trijoin.Record{
        Stream: trijoin.A, Key: "acct-7", Time: 10, RowID: 101, Weight: +1,
    }),
    trijoin.RecordEvent(trijoin.Record{
        Stream: trijoin.C, Key: "acct-7", Time: 11, RowID: 301, Weight: +1,
    }),
    trijoin.RecordEvent(trijoin.Record{
        Stream: trijoin.B, Key: "acct-7", Time: 12, RowID: 201, Weight: +1,
    }),
)

for _, d := range deltas {
    // d.Weight < 0 表示撤回已有三元组。
}

snapshot := j.Snapshot() // 当前已物化的输出关系
stats := j.Stats()
```

也可以逐条调用 `j.Process(record)`，或用 `WatermarkEvent(W)` /
`j.AdvanceWatermark(W)` 推进水位。批量内需要抵消的正负增量应放在同一次
`Apply` / `ApplyBatch` 中。

## 水位与清理

水位只前进不后退。设当前全局水位为 `W`，各流保留事件时间不早于以下安全下界：

| 流 | 安全下界 | 原因 |
|---|---:|---|
| A | `W-d2` | 将来 `t_B >= W`，A 仍可能匹配未来 B 的条件是 `t_A+d2 >= W` |
| B | `W-min(d1,d4)` | B 既要能匹配未来 A（`t_B+d1 >= W`），也要能匹配未来 C（`t_B+d4 >= W`） |
| C | `W-d3` | C 仍可能匹配未来 B 的条件是 `t_C+d3 >= W` |

清理严格删除 `t < 下界` 的状态行；`t == 下界` 仍保留，因为可以与恰好
位于 `W` 的未来事件匹配。清理不会撤销已经物化的输出；`Snapshot` 仍保留
这些三元组。

每个流按自己的下界判断迟到：`t < 下界` 的变更计入 `Stats.LateDrops`，
不改变输入状态或输出关系。处于下界与 `W` 之间、但仍可参与连接的迟到
变更可被接受。

## 统计与确定性

`Stats` 提供：

- `SafeLowerBound`、`ActiveRows`、`TrackedRows`
- `CleanedRows`、`LateDrops`
- `OutputTriples`、`OutputWeight`

`Snapshot` 和批次增量均按 `Key`、`ARow`、`BRow`、`CRow` 排序，便于差分对照。

## 测试

```sh
go test ./...
```

测试包含全历史批处理重算的随机差分对照、撤回先于插入、水位边界、三流不对称
清理、迟到丢弃、单条 B 撤回撤销多条 A×C 组合，以及同批正负增量压缩。
