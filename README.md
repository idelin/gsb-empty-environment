# windowfn — 向量化窗口函数累加器

Go 标准库实现的滑动窗口 SUM / AVG / RANK 向量化算子。列式批量处理：
行先按 (分区键, 排序键) 稳定排序并切分分区边界，累加器在每个分区内
单次扫描，新进值加入累加状态、滑出窗口的值即时扣除，每行 O(1) 摊还，
结果按原始行序写回向量。

## 支持

- `SUM` / `AVG`：`ROWS n PRECEDING`、`RANGE n PRECEDING`（含 peers）、
  `UNBOUNDED PRECEDING`，帧尾均为 `CURRENT ROW`
- `RANK()`：分区内 1 基排名，同值并列并跳号

## 用法

```go
w := windowfn.NewWindow(store, date)              // 分区键 + 排序键列
sum := w.Sum(sales, windowfn.RowsPreceding(10))   // ROWS 10 PRECEDING
avg := w.Avg(sales, windowfn.RangePreceding(6))   // RANGE 6 PRECEDING
rank := w.Rank()
```

## 运行

```sh
go test ./... -v        # 测试（含 10 万行验收用例，对照朴素 O(n·k) 参考实现逐行校验）
go run ./cmd/demo       # 样例：10 万行 SUM(sales) OVER (PARTITION BY store ORDER BY date ROWS 10 PRECEDING)
go test -run=NONE -bench=. .
```
