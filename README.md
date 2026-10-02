# sessionwin：会话超时可撤销合并窗口

按相邻事件时间间隔不超过 `gap` 合并会话窗口，输出 retract/upsert 变更流。
迟到事件可连接原本独立的窗口；删除事件可使窗口分裂；水位线推进后丢弃
超出最大延迟（`maxLateness`）范围的事件状态与窗口。

## API

- `NewMerger(gap, maxLateness)` 创建合并器
- `Add(t) []Change` 事件到达；超出最大延迟的迟到事件被丢弃
- `Remove(t) []Change` 删除事件，可能触发窗口分裂
- `AdvanceWatermark(wm) []Change` 单调推进水位线并清理过期状态
- `Windows()` / `Watermark()` / `EventCount()` 查询当前状态

`Change` 为 `{Type: Retract|Upsert, Window: [Start, End]}`，每次增删事件后
与已发出窗口做 diff：消失的窗口发 `Retract`，新出现的窗口发 `Upsert`。

## 运行

```sh
go test ./... -v   # 测试（含验收边界用例）
go run ./cmd/demo  # 运行样例
```

## 验收边界（gap=5）

1. `t=0`、`t=9` 先后到达 → 两个独立窗口 `[0,0]`、`[9,9]`
2. 迟到事件 `t=4` 到达 → `RETRACT [0,0]`、`RETRACT [9,9]`、`UPSERT [0,9]`
3. 删除 `t=4` → `RETRACT [0,9]`、`UPSERT [0,0]`、`UPSERT [9,9]`（重新分裂）
