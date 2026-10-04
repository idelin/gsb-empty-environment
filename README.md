# streamjoin

双流按键（keyed）事件时间窗口连接库，左右水位独立推进。

## 语义

- **窗口连接**：左事件 `l` 与右事件 `r` 同 key 且
  `l.ts + LowerBound <= r.ts <= l.ts + UpperBound` 时输出连接对。
- **迟到处理**：事件时间落后于本侧水位即为迟到。
  - 仍在更正期限（`AllowedLateness`）内：正常连接，输出标记 `Delta=true` 的差量。
  - 超出更正期限：不参与连接、不缓存，单独记录到 `LateRecords()`。
- **水位与状态清理**：`AdvanceWatermark(side, ts)` 单调推进该侧水位，并仅清理
  **对侧**状态中不可能再被匹配的事件。清理 horizon 包含更正期限：
  左事件 `l` 在 `l.ts + UpperBound + AllowedLateness < 右水位` 时才被丢弃
  （右事件对称使用 `LowerBound`）。因此一侧水位停滞时，另一侧的可匹配
  数据绝不会被提前删除。

## 用法

```go
j := streamjoin.New(streamjoin.Config{
    LowerBound: -5, UpperBound: 5, AllowedLateness: 10,
})
j.Add(streamjoin.Left, &streamjoin.Event{Key: "a", Timestamp: 100})
j.Add(streamjoin.Right, &streamjoin.Event{Key: "a", Timestamp: 103})
j.AdvanceWatermark(streamjoin.Right, 200)

pairs := j.Outputs()      // 连接结果（含 Delta 标记）
late := j.LateRecords()   // 超期事件单独报告
```

## 测试

```sh
go test ./...
```
