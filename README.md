# interval

精确有理区间运算 + 指定位数十进制包围(Go, 仅依赖 `math/big`)。

## 核心概念

- `Interval`:单段区间,端点为精确 `*big.Rat` 或 ±∞,带开闭标记;空集规范化表示,单点 `[a,a]` 不为空。
- `Set`:不相交有序区间集合,`MaxSegments > 0` 时段数超限按最近间隙做 hull 保守合并(结果只会变大,绝不丢点)。
- `Enclose(digits)`:左端向下、右端向上舍入到 `10^-digits`,开闭标记保留且不破坏包含关系;无穷端保持无穷。

## 示例

```go
x := interval.Closed(big.NewRat(1, 3), big.NewRat(2, 3))
enc := x.Enclose(2) // [0.33, 0.67],包含精确区间

// 跨零除法产生多段:(-∞, -1] ∪ [1, +∞)
s := interval.Closed(big.NewRat(1, 1), big.NewRat(2, 1)).
    Div(interval.Closed(big.NewRat(-1, 1), big.NewRat(1, 1)))

// 段数上限 = 2,超限只做保守合并
set := interval.NewSet(2)
set.Add(x)
```

## 测试

```sh
GO111MODULE=on go test ./...
```

覆盖:有理点包含与边界验证、负数、无穷端、空集、单点非空、开闭标记、跨零除法多段、段数上限保守合并。
