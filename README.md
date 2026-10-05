# ratsimp

有理函数化简库（Go，精确算术）。化简**保留原定义域**：约去公因子只是
消去了可去间断点的"表现"，原函数在该点仍然没有定义。

## 功能

- **约分**：输出互素的分子分母（整系数、去公内容、分母首项系数为正）。
- **原定义域排除点**：列出原分母的所有实根，区分
  - `Hole`（可去间断点 / 孔洞）：因子被完全约去，但原函数在此无定义；
  - `Pole`（极点）：约分后仍然存在的奇点，并给出**极点重数**
    （`PoleOrder`）及其在原分母中的重数（`OriginalMultiplicity`）。
- **精确根隔离**：每个排除点用有理端点区间 `(Lo, Hi]` 表示，区间内恰含
  一个根（基于 Sturm 序列的实根隔离，全程 `big.Rat` 精确运算），
  可用 `Refine` 任意缩小区间宽度。
- **等价判断**两种模式：
  - `SameDomain`：定义域相同（排除点集合一致）且在其上取值相等；
  - `CommonDomain`：只在公共定义域上要求取值相等（允许一方有孔洞）。

## 用法

```go
xm1 := ratsimp.PolyFromInts(-1, 1)                    // x - 1
num := xm1.Pow(2).Mul(ratsimp.PolyFromInts(2, 1))     // (x-1)^2 (x+2)
den := xm1.Pow(3).Mul(ratsimp.PolyFromInts(-3, 1))    // (x-1)^3 (x-3)
r, _ := ratsimp.NewRational(num, den)
s := ratsimp.Simplify(r)
fmt.Print(s)
// reduced: (x + 2) / (x^2 - 4x + 3)
// excluded from original domain:
//   x in (3/4, 3/2] ~ 1.125: pole of order 1 (original multiplicity 3)
//   x in (9/4, 3] ~ 2.625: pole of order 1 (original multiplicity 1)
```

等价判断：

```go
f1, _ := ratsimp.NewRational(ratsimp.PolyFromInts(-1, 0, 1), ratsimp.PolyFromInts(-1, 1)) // (x^2-1)/(x-1)
f2, _ := ratsimp.NewRational(ratsimp.PolyFromInts(1, 1), ratsimp.PolyFromInts(1))          // x+1
ratsimp.Equivalent(f1, f2, ratsimp.CommonDomain) // true：在公共定义域上相等
ratsimp.Equivalent(f1, f2, ratsimp.SameDomain)   // false：f1 在 x=1 有孔洞
```

## 实现要点

- 多项式为整系数（`math/big.Int`），GCD / 平方自由分解（Yun 算法）/
  Sturm 序列在有理数域（`big.Rat`）上进行，无浮点误差。
- 零点重数来自分母与 gcd 的平方自由分解：根在某因子 `f_i` 中即重数为 `i`；
  极点重数 = 原分母重数 − 被约去的重数，为 0 即为孔洞。
- 零分子是合法输入：`0/(x-1)` 化简为 `0/1`，`x=1` 仍记为孔洞；
  零分母返回错误。

## 测试

```sh
go test ./...
```

覆盖孔洞保留、重复因子（部分约去后仍是极点）、零分子、两种等价模式、
无理根的精确隔离与细化。
