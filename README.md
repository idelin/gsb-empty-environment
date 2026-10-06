# windowagg

监控窗口聚合库：维护一个 int64 多重集合，支持插入与撤销（重复值各自计数），
并输出精确统计量。

## API

- `Insert(v)` / `Revoke(v) bool`：插入 / 撤销一个值；撤销不存在的值返回 `false`，窗口不变。
- `Len() int` / `Sum() *big.Int`：计数与精确总和（大整数，不溢出）。
- `Median() (*big.Rat, bool)`：精确中位数；偶数个时取两中值的均值（约分有理数）；空窗返回缺失。
- `TrimmedMean(k) (*big.Rat, bool)`：去掉最小、最大各 k 项后的均值，返回约分有理数；
  `k < 0` 或数量不足 `2k+1` 时返回缺失。

## 实现

随机 treap 有序统计树：节点按值去重并计数，子树维护元素个数与 `big.Int` 和，
插入 / 撤销 / 查询均为 O(log n)。`Window` 零值即可用，非并发安全。

## 测试

`window_test.go` 用朴素有序数组模型交叉核对：重复边界值、空窗、大整数总和，
以及小规模重复值与 int64 极值两组随机操作序列（每步比对 Len/Sum/Median/TrimmedMean）。

```sh
go test ./...
```
