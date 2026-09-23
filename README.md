# polysub —— 多变量多项式的同时代换

纯 Python 3.11 标准库实现（`fractions.Fraction` 精确有理数），不依赖任何
符号代数库。核心目标：

- **同时代换**：先构造完整映射再一次性求值，避免顺序代换污染；
  `{x: y, y: x}` 是交换，而非全部变成 `x`。
- **膨胀估计 + 项数预算**：展开前按上界估计项数，超预算即停，
  保留未展开的 `Expr` 形式，绝不返回截断的假等式。
- **零代换消项**：替换值为 0 时，含该变量正次幂的项被消去。

## 文件

- `polysub.py` —— 库本体（`Poly` / `Expr` 体系）
- `demo.py` —— 运行样例（`python3.11 demo.py`）
- `tests/test_polysub.py` —— unittest 测试（32 例）

## 快速上手

```python
from fractions import Fraction
from polysub import Poly, BudgetExceeded

x, y = Poly.var("x"), Poly.var("y")
a, b = Poly.var("a"), Poly.var("b")

p = x**2 + x*y + 2
p.substitute({"x": y, "y": x})        # 同时交换 → x*y + y^2 + 2
p.substitute({"x": 0})                # 零代换消项 → 2
(3*x**2).substitute({"x": Fraction(1, 3)})  # 精确 → 1/3

big = x**5
deferred = big.substitute({"x": a + b}, budget=5)   # 超预算 → 未展开 Expr: (a + b)^5
done     = big.substitute({"x": a + b}, budget=6)   # 预算内 → 展开为 Poly
deferred.expand() == done                            # True（事后展开仍是真值）
```

## 各变换的适用域

| 变换 | 适用域 |
| --- | --- |
| `Poly.constant / Poly.var` | 系数为任意有理数（`int`/`Fraction`）；浮点一律拒绝，避免精度污染 |
| `+ - *` | 操作数为 `Poly` 或有理数；变量集合自动取并集对齐 |
| `** n` | 仅非负整数指数（多项式环内运算）；负指数属有理函数域，不支持；`0^0` 按空积约定为 1 |
| `substitute(mapping)` | 键必须是被代换多项式的变量名（否则 `ValueError`）；值为 `Poly`/有理数；“同时”语义：替换式中的变量不会被二次代换 |
| `substitute(..., budget=n)` | 估计项数 ≤ n 才展开返回 `Poly`；否则返回未展开 `Expr`（精确值的符号形式） |
| `Expr.expand(budget=n)` | 估计超预算抛 `BudgetExceeded`（异常携带原表达式可重试），不产出截断结果 |
| `Expr.term_estimate()` | 安全上界：`+` 取项数和，`*` 取项数积，`P^n` 取可重复组合数 `C(t+n-1, n)`；同类项合并只会使实际更少 |

## 运行样例（`python3.11 demo.py`）

```
=== 1. 同时代换：x→y、y→x 是交换，不是全部变成 x ===
p                      = x^2 + x*y + 2
p[x→y, y→x]（同时）    = x*y + y^2 + 2
p 先 x→y 再 y→x（顺序）= 2*x^2 + 2  ← 顺序代换的污染结果

=== 2. 替换式中的变量不会被二次代换 ===
q                      = x - y
q[x→y+1, y→x+1]        = -x + y

=== 3. 代换零消项 ===
r                      = x^2 + x*y + y + 3
r[x→0]                 = y + 3  ← 含 x 的项消去，常数项保留

=== 4. 精确有理数（无浮点误差） ===
3x^2 在 x=1/3 处       = 1/3
(x + 1/2)^2            = x^2 + x + 1/4

=== 5. 项数预算：估计膨胀，超预算保留未展开形式 ===
预算 5： (a + b)^5  ← 未展开的 Expr，不是截断假等式
expand(budget=5) 抛出: 展开估计需要 6 项，超出预算 5 项；已保留未展开形式
预算 6： a^5 + 5*a^4*b + 10*a^3*b^2 + 10*a^2*b^3 + 5*a*b^4 + b^5
事后充足预算展开       = a^5 + 5*a^4*b + 10*a^3*b^2 + 10*a^2*b^3 + 5*a*b^4 + b^5
与直接展开相等： True

=== 6. 更大的膨胀估计 ===
x^10[x→a+b+c]，预算 50： (a + b + c)^10
估计项数 = 66 ；实际展开项数 = 66
```

## 测试结果（`python3.11 -m unittest discover`）

```
Ran 32 tests in 0.012s

OK
```

覆盖：同时交换/防顺序污染、替换式不二次代换、零消项（含常数保留、全消）、
精确有理数（分数合并、二项式系数、1/3 代入）、预算内展开、超预算保留
未展开形式、事后展开与直接展开一致、`BudgetExceeded` 不截断、估计上界
性质、变量对齐、非法输入（浮点/负指数/未知变量）拒绝等。
