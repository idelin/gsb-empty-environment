# interval — 严密区间算术包络机

基于 `math/big.Rat` 精确有理数端点的区间数值计算器（仅标准库）。
每个运算返回的闭区间保证包含真实数学结果集合。

## 特性

- 区间 `[Lo, Hi]`，端点为精确有理数，无浮点舍入外溢
- 运算：`Add` / `Sub` / `Neg` / `Mul` / `Recip` / `Div` / `Sqrt`
- `Sqrt` 对无理根做外扩取整：下端向下、上端向上（`isqrt(p*q)/q`）
- 除数区间包含 0 时返回 `ErrZeroDivisor`（不可定域异常）
- 下端为负时 `Sqrt` 返回 `ErrNegativeRadicand`

## 使用

```go
a := interval.OfInt64(1, 2)
b := interval.OfInt64(3, 4)
a.Mul(b)        // [3, 8]
q, err := a.Div(b) // [1/4, 2/3], err == nil
_, err = a.Div(interval.OfInt64(-1, 1)) // err == interval.ErrZeroDivisor
```

## 运行

```sh
go test ./...        # 运行测试
go run ./cmd/example # 运行样例
```
