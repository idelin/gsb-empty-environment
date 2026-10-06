# erranalysis/interval

有理数区间运算库（误差分析）：输入有理数区间，执行加减乘除，
输出指定十进制精度、严格包含真值的区间。

## 设计

- 端点用 `math/big.Rat` 精确表示，四则运算全程精确，无浮点误差。
- 乘法采用四端点模型：四个端点乘积取 min/max。
- 除法：除数区间**包含零即返回无界标记**（`Unbounded`），
  无论零在区间内部还是端点（`ContainsZero` 判定 `Lo <= 0 <= Hi`）。
- 仅在 `Format(prec)` 输出时做十进制舍入，且两端分别向外：
  下界向 -∞（floor）、上界向 +∞（ceil），保证输出区间仍包含真值。

## 用法

```go
a := interval.New(rat("1/3"), rat("2"))   // [1/3, 2]
b := interval.Parse("-3/4")               // 退化区间 [-3/4, -3/4]
c := interval.Mul(a, b)                   // [-3/2, -1/4]
fmt.Println(c.Format(4))                  // [-1.5000, -0.2500]

d := interval.Div(a, interval.New(rat("-1"), rat("1")))
fmt.Println(d.Format(2))                  // (-inf, +inf)，除数含零
```

## 测试

```sh
go test ./...
```

测试以精确有理四端点模型核对：负数区间、退化区间、跨零区间、
连续运算 `((a+b)*c-d)/e` 的采样点包含性，以及输出向外舍入方向
（含负数舍入方向与退化区间两端各自向外）。
