# rational

有理函数化简库（Go，精确有理数运算，无浮点误差）。

核心原则：**约分不改变原函数的定义域**。约掉公因子只是得到代数上
相等的既约表达式，原函数在被约去的根处仍然没有定义——这些可去间断点
（孔洞）必须保留。

## 功能

- `Simplify(f)` 返回 `Simplified`：
  - `Num` / `Den`：既约分子、分母（分母首一化，gcd = 1）
  - `Holes`：原定义域中的可去间断点（分子在该根的重数 ≥ 分母重数）
  - `Poles`：既约后的极点及重数（分母重数 − 分子重数）
  - `Excluded`：原分母的全部实根（孔洞 ∪ 极点位置），即原定义域的排除点
- 根用 `IsolatedRoot` 精确表示：平方自由多项式 + 有理端点隔离区间
  `(Lo, Hi)`，可用 `RefineTo` 任意细化，永无舍入误差（Sturm 序列 +
  区间二分，Cauchy 界起步）。
- `Equivalent(f, g, mode)` 函数等价判断：
  - `SameDomain`：代数相等 **且** 定义域相同（分母实根集合一致）
  - `CommonDomain`：只要求在公共定义域上相等（代数相等即可，孔洞可不同）

## 示例

```go
// f = (x-1)(x-2) / ((x-1)(x-3))：x=1 是孔洞，x=3 是极点
f := rational.RationalFunction{
    Num: rational.PolyFromInts(2, -3, 1),
    Den: rational.PolyFromInts(3, -4, 1),
}
s, _ := rational.Simplify(f)
// s.Num = x-2, s.Den = x-3
// s.Holes = [包含 1 的隔离区间], s.Poles = [{根含 3, 重数 1}]
```

## 测试

```sh
go test ./...
```

覆盖：孔洞保留、重复因子（部分约分仍是极点）、零分子、二分恰好命中
有理根（x=1/2）、无理根（±√2）精确隔离、两种等价模式、零分母拒绝。
