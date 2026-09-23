# 有理 Newton/Hermite 插值

本项目用 Python 3.11 标准库实现精确有理插值，不依赖 SymPy 等符号代数库。所有节点、函数值、导数值和系数都保存为 `fractions.Fraction`。

## 功能

- 增量加入或删除函数值观测 `p(x)=y`。
- 增量加入或删除一阶 Hermite 导数观测 `p'(x)=d`。
- 同一 `x` 的重复同值观测幂等；不同右端立即报冲突并给出见证。
- 输出 Newton 节点/系数，或规范幂基低次到高次系数 `(a0, a1, ...)`。
- 复用按活动观测编号索引的差商区间；删除任意观测后重建受影响区间，而不是简单删除最后一个系数。
- 独立幂基线性方程求解器核验全部值和导数约束，并诊断唯一、欠定或不相容。
- 对不相容线性条件输出有理线性组合见证及非零残差。
- 每次状态变化记录输入、Newton 系数和幂基系数的实际分子/分母位长。
- 不同节点组顺序产生不同 Newton 表示，但转换为幂基后数学等价。

## 每步变换及适用域

| 变换 | 规则 | 适用域与诊断 |
| --- | --- | --- |
| 有理输入 | `int`、`Fraction`、精确分数字符串转换为 `Fraction` | 拒绝 `float`，避免二进制浮点误差 |
| 值观测 | 每个 `x` 至多一个 `p(x)=y` | 同值重复返回原观测；不同值抛出 `ConflictingObservations` |
| 导数观测 | 每个 `x` 至多一个 `p'(x)=d` | 同值重复幂等；不同导数值冲突；暂不支持二阶及以上导数 |
| 节点展开 | 有值和导数的组展开为相邻 `[x, x]`；只有值展开为 `[x]` | 组间顺序可任意排列；组内固定为值节点、导数节点 |
| 汇差商 | 端点不同用 `(D[i+1:j]-D[i:j-1])/(x_j-x_i)`；相邻重复端点用导数 | 完整一阶 Hermite 域；更长重复跨度会提示需要更高阶导数 |
| Newton 输出 | 前缀差商作为 Newton 系数 | 要求每个导数节点都有同 `x` 的值节点 |
| 幂基转换 | 逐项乘以 `(x-x_i)` 并精确求和 | 对任意已构建的 Newton 形式有效 |
| 增量加/删 | 缓存键由活动观测编号组成，未受影响区间继续复用 | 删除中间或非末尾观测会重建后缀区间，不截断系数表 |
| 节点重排 | `reorder_nodes` 只排列不同 `x` 的组 | 幂基结果不变；Newton 节点和系数可相应改变 |
| 独立线性求解 | `n` 个任意值/一阶导数约束对应次数 `< n` 的未知量 | 可诊断 Newton 域外数据，例如导数节点没有对应值节点 |
| 全部约束核验 | 比较所有值残差、导数残差及独立求解器系数 | Newton 形式和线性参考必须同时通过 |
| 位长记录 | 对规范化 `Fraction` 取 `numerator.bit_length()` 和 `denominator.bit_length()` | 每次加、删、重排后记录输入、Newton 和幂基最大位长 |

## API 示例

```python
from fractions import Fraction
from rational_interpolation import RationalHermiteInterpolator, format_power

interp = RationalHermiteInterpolator()
interp.add_point(0, 0)
interp.add_derivative(0, 0)
interp.add_point(1, 1)
interp.add_derivative(1, 3)

nodes, newton_coefficients = interp.to_newton()
power_coefficients = interp.to_power()
assert format_power(power_coefficients) == "x^3"
assert interp.verify().passed

interp.add_point(Fraction(1, 2), Fraction(1, 8))
interp.remove_point(Fraction(1, 2))
assert interp.verify().passed
```

## 运行样例

```bash
python3.11 demo.py
```

实际输出：

```text
== Hermite interpolation: p=x^3 ==
Newton nodes: (Fraction(0, 1), Fraction(0, 1), Fraction(1, 1), Fraction(1, 1))
Newton coefficients: (Fraction(0, 1), Fraction(0, 1), Fraction(1, 1), Fraction(1, 1))
Newton form: x*x + x*x*(x - 1)
power form: x^3
verification passed: True
value residuals: {Fraction(0, 1): Fraction(0, 1), Fraction(1, 1): Fraction(0, 1)}
derivative residuals: {Fraction(0, 1): Fraction(0, 1), Fraction(1, 1): Fraction(0, 1)}

== Node order equivalence ==
reordered nodes: (Fraction(1, 1), Fraction(1, 1), Fraction(0, 1), Fraction(0, 1))
power basis unchanged: True

== Rational input and bit growth ==
power coefficients: (Fraction(0, 1), Fraction(16, 21), Fraction(-4, 21))
power form: 16/21*x - 4/21*x^2
verification passed: True
bit growth: input=(3, 3) newton=(3, 5) power=(5, 5)

== Removing a middle observation ==
before removal: x + 5/2*x^2 - 1/2*x^3
after removing p(1): x + x^2
verification passed: True

== Conflict and incompatibility certificates ==
conflict witness: {'existing#1': Fraction(1, 1), 'incoming#2': Fraction(-1, 1)}
conflict residual: -1
linear status: inconsistent
linear witness: {0: Fraction(1, 1), 1: Fraction(-1, 1)}
linear residual: -1

== Derivative-only data outside the Newton domain ==
Newton domain error: derivative at x=0 has no matching value constraint; confluent Newton form is outside its domain
independent diagnosis: {'status': 'unique', 'rank': 2, 'unknowns': 2, 'coefficients': (Fraction(1, 1), Fraction(1, 1)), 'witness': None, 'residual': None, 'newton_domain_error': 'derivative at x=0 has no matching value constraint; confluent Newton form is outside its domain'}
```

## 测试结果

```bash
python3.11 -m unittest -v
```

实际结果：

```text
test_derivative_only_data_is_diagnosed_by_linear_reference ... ok
test_duplicate_same_value_is_idempotent_and_conflict_is_diagnosed ... ok
test_exact_input_contract_and_formatting ... ok
test_hermite_repeated_nodes_and_derivatives ... ok
test_incremental_newton_and_power_forms ... ok
test_independent_solver_solves_and_certifies_inconsistency ... ok
test_node_order_permutation_is_mathematically_equivalent ... ok
test_rational_bit_growth_is_recorded ... ok
test_remove_derivative_and_readd_it_incrementally ... ok
test_remove_middle_observation_rebuilds_not_truncates ... ok

Ran 10 tests in 0.003s

OK
```

## 文件

- `rational_interpolation.py`：插值器、精确线性求解器、验证、诊断、格式化和位长统计。
- `test_rational_interpolation.py`：标准库 `unittest` 测试。
- `demo.py`：可重复运行的样例。
