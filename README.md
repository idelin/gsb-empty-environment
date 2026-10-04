# sparseprefix

稀疏前缀和库（Go）：面向"数组很长但只有少数非零区间"的场景，支持区间赋值与按累计量反查最小下标。

## 数据表示

- 数组下标为非负 `int64`，值为 `uint64`。
- 内部维护按起点排序、互不重叠的分段 `[lo, hi] -> val`：
  - 空段（值为 0）不保留；
  - 相邻等值段自动合并。
- 分段数量少，采用有序切片 + 二分查找，赋值为 O(分段数)。

## API

- `Assign(lo, hi, val) error`：将 `[lo, hi]`（含端点）赋为 `val`；`val == 0` 表示清除。`lo > hi` 或 `lo < 0` 返回 `ErrInvalidRange`。
- `ValueAt(i) uint64`：单点查询，空档处为 0。
- `PrefixSum(i) *big.Int`：区间 `[0, i]` 的前缀和（`i < 0` 时为 0）。
- `Total() *big.Int`：全数组总和。
- `FindIndex(cum uint64) (int64, bool)` / `FindIndexBig(cum *big.Int) (int64, bool)`：
  返回满足"前缀和 ≥ cum"的最小下标。
  - `cum` 超过总和时返回 `(0, false)`，明确表示不存在；
  - `cum == 0` 按定义匹配下标 0；负数（仅 Big 版）不匹配。

## 算术安全

所有累计量（分段和、前缀和、总和、反查比较）均使用 `math/big` 计算，
分段长度与取值即使接近 `int64`/`uint64` 上限也不会溢出。

## 测试

`sparseprefix_test.go` 以小型稠密数组为参考模型逐项核对：

- 多种子随机区间赋值（含清零），每次操作后逐点比对 `ValueAt`、`Total`，
  并对 0、1、总和、总和+1 及每个前缀和边界 ±1 核对 `FindIndex`；
- 零段被清除、恰好落在段边界、反复分裂/合并；
- 总值超过 64 位时的反查与越界判定；
- 不变式检查：有序不重叠、无零值段、无相邻等值段。

运行：

```sh
go test ./...
```
