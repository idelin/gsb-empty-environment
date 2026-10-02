# windowfn — 向量化窗口函数累加器

Go 标准库实现的滑动窗口 SUM / AVG / RANK 向量化算子。输入为按
`(分区键, 排序键)` 排好序的列存批次，输出为结果列向量。

## 设计

- **批量分区处理**：`partitions` 一次扫描划分分区边界，每个分区内独立维护累加状态。
- **滑动累加**：窗口边界随当前行单调前移，进入窗口的值累加、滑出窗口的值
  扣除，每行均摊 O(1)，整体 O(n)，而非对每行重新求和的 O(n·k)。
- **ROWS / RANGE 两种帧规格**：
  - `ROWS`：按物理行偏移取窗（如 `ROWS 10 PRECEDING`）。
  - `RANGE`：按排序键的逻辑距离取窗（如 `RANGE 10 PRECEDING`，自动包含对等行/并列）。
- **帧边界**：支持 `UNBOUNDED PRECEDING / n PRECEDING / CURRENT ROW /
  n FOLLOWING / UNBOUNDED FOLLOWING` 任意组合；空帧 AVG 返回 NaN（对应 SQL NULL）。
- **RANK**：分区内按排序键计算 1 基名次，并列共享名次并跳号。

## 布局

- `window/frame.go` — 帧规格（Mode / Bound / Frame）与合法性校验
- `window/window.go` — 分区扫描、滑动累加器、Sum / Avg / Rank 算子
- `window/window_test.go` — 单元测试 + 10 万行验收测试 + 基准
- `cmd/demo/main.go` — 运行样例

## 运行

```sh
go test ./...        # 测试（含 10 万行验收用例，与朴素实现逐行对拍）
go test -bench=. ./window/   # 基准：滑动累加 vs 朴素重算
go run ./cmd/demo    # 样例：SUM(sales) OVER (PARTITION BY store ORDER BY date ROWS 10 PRECEDING)
```

## 示例

```go
in := &window.Input{
    PartitionKeys: []string{"a", "a", "a"},
    OrderKeys:     []float64{1, 2, 3},
    Values:        []float64{10, 20, 30},
}
sums, _ := window.Sum(in, window.Frame{
    Mode:  window.Rows,
    Start: window.Bound{Kind: window.Preceding, Offset: 1},
    End:   window.Bound{Kind: window.CurrentRow},
})
// sums = [10, 30, 50]
```
