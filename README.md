# interval_arith — 精确有理区间算术与仿射相关表示

纯 Python 3.11 标准库实现（`fractions` + `math`），不使用任何符号代数库。
所有内部计算均为**精确有理数**，无舍入误差；唯一出现舍入的地方是导入
不精确数据（float）时，此时**向外保守舍入**（`Interval.from_float` 向两侧
各扩一个 ulp），保证区间始终包含真实值。

## 设计要点

- **四则 / 平方 / 单调函数**：`Interval` 支持 `+ - *`，`square()` 与
  `apply_monotone(fn, increasing)`（前提：`fn` 在整个区间上单调，像恰为
  端点像）。`[-1,1].square()` 恰为 `[0,1]`。
- **除数跨 0**：`divide` 返回 `DivResult`——跨 0 时给出**不连续域**
  （`Domain`，两条射线，如 `1/[-1,2] = (-∞,-1] ∪ [1/2,+∞)`）；除数恰为
  `[0,0]` 时返回 **undefined**。除零点绝不隐藏：结果的 `note` 字段明确
  记录适用域与"0 本身不在定义域内"。
- **仿射相关表示**：表达式中同一变量的重复引用共享噪声符号
  `value = const + Σ c_s·e_s + remainder`，线性部分精确（`x - x = 0`、
  `x*x` 在 `[-1,1]` 上恰为 `[0,1]`），非线性余项用可验证的保守界
  （`e_s²∈[0,1]` 精确，交叉项 `e_s·e_t∈[-1,1]`，余项经精确区间算术复合）。
- **直接相乘相同区间**：朴素区间乘法把两个因子当作独立，得到保守结果
  （`[-1,1]*[-1,1]=[-1,1]`），库在文档与测试中明确说明这一相关性丢失；
  需要精确结果时改用 `square()` 或仿射语义。
- **奇点分裂与预算**：`analyze(eval_fn, domain, singularities, budget)`
  在奇点处分裂定义域；预算耗尽时，未分析的区间**保留**在
  `result.unanalyzed` 中，绝不丢弃。
- **证书验证**：每步变换记录 `Step(rule, domain_note, inputs, output)`，
  `Certificate.verify()` 用精确有理数重放全部推导并检查最终的包含声明；
  采样**只**用于寻找反例（`find_counterexample`），不能以"采样没越界"
  证明包含性——包含性只能由证书证明。

## 文件

- `interval_arith/core.py` — 区间、不连续域、除法、单调函数、保守舍入
- `interval_arith/affine.py` — 仿射形式（线性精确 + 余项保守）
- `interval_arith/expr.py` — 表达式树（区间语义 / 仿射语义）
- `interval_arith/certificate.py` — 推导证书与重放验证
- `interval_arith/splitting.py` — 奇点分裂 + 分析预算
- `interval_arith/sampling.py` — 有理采样（仅反例检查）
- `demo.py` — 运行样例；`test_interval_arith.py` — 28 个 unittest

## 运行样例（`python3 demo.py` 实际输出）

```
=== 1. [-1, 1]^2 = [0, 1] (square is exact) ===
square:       [0, 1]
naive x*x:    [-1, 1]  (conservative: treats factors as independent)
affine x*x:   [0, 1]  (correlation tracked: e^2 in [0, 1])

=== 2. Division by an interval straddling 0 -> disconnected domain ===
1 / [-1, 2] = [-inf, -1] ∪ [1/2, inf]   [divisor straddles 0: result is a disconnected domain; the point 0 itself is excluded from the domain of definition]
1 / [0, 2]  = [1/2, inf]   [divisor straddles 0: ...]
1 / [0, 0]  = undefined (divisor is exactly [0, 0]: division undefined)

=== 3. Monotone function (cube, exact) ===
[-2, 3]^3 = [-8, 27]

=== 4. Conservative outward rounding when importing floats ===
from_float(0.1) = [7205759403792793/72057594037927936, 7205759403792795/72057594037927936]
from_decimal('0.1') = [1/10, 1/10] (exact)

=== 5. Affine correlation: x - x and x*(x+1) ===
x - x, affine:   [0, 0]
x - x, interval: [-2, 2]
x*(x+1) on [0,1], affine:   [-1/4, 2]
x*(x+1) on [0,1], interval: [0, 2]

=== 6. Certificate: every step states its applicable domain ===
1. [introduce] introduce noise symbol e_x in [-1, 1]; exact reparametrization of [-1, 1]
2. [introduce] introduce noise symbol e_x in [-1, 1]; exact reparametrization of [-1, 1]
3. [affine_mul] linear part exact; e_s^2 bounded in [0, 1], cross terms e_s*e_t in [-1, 1], remainders composed by exact interval arithmetic; valid for all noise values in [-1, 1]
4. [range] range = exact linear radius + conservative nonlinear remainder
5. [inclusion] claimed bound must contain the certified range
certified range: [0, 1] | verify(): True

=== 7. Splitting at the singularity of 1/x, with a budget ===
  1/x on [-2, 0]: [-inf, -1/2]
  1/x on [0, 3]: [1/3, inf]
budget=1 -> analyzed: ['[-2, 0]'] | unanalyzed (kept): ['[0, 3]']

=== 8. Rational sampling: counterexamples only, never a proof ===
claimed [-1/2, 1/2] for x^2 on [-1,1]: counterexample x = -1, x^2 = 1
claimed [0, 1]: no counterexample found (None) -> NOT a proof; inclusion is proved by the certificate in (6)
```

## 测试结果（`python3 -m unittest test_interval_arith`）

```
Ran 28 tests in 0.003s

OK
```

覆盖：精确四则与平方（含 `[-1,1]²=[0,1]`）、朴素乘法保守性、跨 0 除法的
不连续域与 `[0,0]` 未定义、单调函数、float 外向舍入、仿射自相消/自乘、
证书验证/拒绝错误界/检测篡改、奇点分裂与预算保留未分析区间、有理采样
反例（找到反例 / 无反例不构成证明）。
