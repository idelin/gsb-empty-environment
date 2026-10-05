# streamjoin — 双流窗口连接库

按 Key 配对左右两流事件，仅当事件时间差 `|l.TS - r.TS| <= Window` 时连接。

## 语义

- **独立水位**：左右两侧各自维护水位（`AdvanceWatermark`），单调递增，互不影响。
- **迟到事件**：事件时间 `<=` 本侧水位即为迟到。
  - 仍在更正期限内（`TS > 水位 - AllowedLateness`）：正常参与连接，产出结果标记 `Delta=true`（追加差量）。
  - 超出更正期限：不参与连接，单独记入 `LateReports()`。
- **状态清理**：左状态只依据右侧水位清理，右状态只依据左侧水位清理。
  左事件 `ts` 仅当 `ts + Window < 右水位 - AllowedLateness` 时才被删除，
  因此一侧水位停滞时，另一侧水位再高也不会提前删除可匹配数据。

## 用法

```go
j := join.New(10000, 5000) // 窗口 10s，更正期限 5s（毫秒）
j.Add(join.Left, join.Event{Key: "u1", TS: 1000, Value: "click"})
pairs := j.Add(join.Right, join.Event{Key: "u1", TS: 1500, Value: "pay"})
j.AdvanceWatermark(join.Left, 2000)
for _, p := range j.Pairs()       { /* 全部结果，p.Delta 标记差量 */ }
for _, r := range j.LateReports() { /* 超期迟到事件，单独上报 */ }
```

## 测试

```sh
GOCACHE=/tmp/gocache GO111MODULE=on go test ./...
```
