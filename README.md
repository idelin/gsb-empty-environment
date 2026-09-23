# 精确单位代数与未知量纲求解

这是一个仅使用 Python 3.11 标准库的单位类型检查与转换示例。实现不调用 SymPy 等符号代数库，也不使用浮点数判断单位指数或方程可解性。

## 功能

- 用整数指数向量表示乘幂单位，例如 `m / s**2` 表示为 `{L: 1, T: -2}`。
- 将未知单位的每个基量纲坐标作为整数未知量，使用 Smith 标准形消元。
- 有解时返回 `particular + Z-span(basis)` 形式的整数解格；欠约束时返回自由维度参数。
- 无解时返回不相容证据，例如 `2*x_L = 1` 不会被浮点近似为 `0.5` 后放行。
- 对解格和每个基向量独立代回全部原始约束，保存逐方程残差。
- 比例单位和绝对仿射单位分开建模；绝对摄氏温度默认不能乘、除或取幂，温差单位可以参与。
- 转换图记录每次增删单位/转换后的版本，旧版本快照仍可查询。
- 检测转换矛盾环，并用精确样例代入给出比例/偏移残差。
- 所有比例、偏移和转换值都使用 `fractions.Fraction`；每个转换步骤都带适用域说明。

## 文件结构

- `units/linear.py`：整数矩阵、Smith 标准形、整数解格、不相容证据、独立代入验证。
- `units/core.py`：整数指数向量、单位表达式、仿射类型规则、未知量纲约束系统。
- `units/graph.py`：精确有理数转换、版本化图、适用域说明、矛盾环检测。
- `demo.py`：可运行示例。
- `tests/test_units.py`：19 个 unittest 测试。

## 快速示例

```python
from units import DimensionVector, UnitConstraintSystem, UnitRegistry

registry = UnitRegistry()
registry.define_base_dimension("L")
registry.define_base_dimension("T")
registry.define_unit("m", {"L": 1})
registry.define_unit("s", {"T": 1})

speed = registry.unit("m") / registry.unit("s")
assert (speed * registry.unit("s")).dimension.exponents == {"L": 1}

system = UnitConstraintSystem(["L", "T"], ["x"])
x = DimensionVector.symbol("?x")
system.equate(x * DimensionVector.symbol("T"), DimensionVector.symbol("L"))
solution = system.solve()
assert solution.dimension_of("x").evaluate(()).exponents == {"L": 1, "T": -1}
```

运行完整示例：

```bash
python3.11 demo.py
```

输出：

```text
速度 × 时间 = L
绝对温度乘法被拒绝: 绝对仿射单位不能参与普通乘法；左仿射=True，右仿射=False，左量纲=Theta，右量纲=T
温差 / 时间 = T^-1 * Theta
未知单位 x 的量纲: L^(1), T^(-1)
原始约束代入残差: (0, 0)
自由维度参数: ('k1',)
u 的自由量纲: L^(1 + 1*k1)
整数不相容证据: 原始约束的整数组合给出 [2] = 1，左侧必被 2 整除而右侧不能
20 degC = 5863/20 K
转换步骤:
degC -> K: y = 1*x + 5463/20；适用域：摄氏绝对温度；可加减和求差，不参与乘除
图版本: 7 -> 9
旧版本包含 km: False
删除 km 后版本: 10
矛盾环 A -> B -> C -> A：复合比例残差 -1/7，复合偏移残差 0
```

## 测试结果

使用 Python 3.11.15 执行：

```bash
python3.11 -m unittest discover -s tests -v
```

结果：

```text
Ran 19 tests in 0.002s

OK
```

覆盖点包括：Smith 标准形重构、整数解格、自由维度、整除性矛盾证据、速度乘时间、整数指数拒绝分数指数、绝对/差量温度规则、未知仿射量联合类型检查、精确有理数转换、版本快照、单位删除、矛盾环和一致环。
