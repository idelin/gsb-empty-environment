# unitchk — 单位/量纲联合类型检查与整数精确约束求解

纯 Python 3.11+ 标准库实现（仅 `unittest` 测试），不调用任何符号代数库，
全程有理数精确计算（`fractions.Fraction` / 整数），不使用浮点近似指数。

## 特性

- **整数指数向量**：乘幂单位表示为基维度上的整数指数向量（如 速度 = `L·T^-1`）。
- **比例与偏移分离**：比例单位（ratio）与仿射绝对单位（absolute，如绝对温度）分开；
  绝对量默认不参与普通乘除/乘幂，两绝对量之差是差量（比例量），可参与运算。
- **联合类型检查**：未知量纲与仿射偏移联合检查；未知单位可分配**自由维度**。
- **整数精确求解**：单位方程 → 整数线性系统 `A x = b`，经列幺模变换（`A·U = H`）
  消元，给出**整数解格**（特解 + 格基 + 自由变量）或**不相容证据**：
  - 有理不相容 → 整数证书 `c`（`c·A = 0` 且 `c·b ≠ 0`）；
  - 整数不相容 → 整除性证据（pivot 方程要求 `p | rhs` 失败，含 `U`、`H` 供复核）。
- **版本化转换图**：增删单位定义/换算边时版本号 +1，记录日志与快照，可查询历史版本；
  新边与已有路径构成**矛盾环**（复合仿射映射 ≠ 恒等）时拒绝并给出环证据。
- **独立代入验证**：`verify` 模块不复用求解器内部状态，从原始输入重算——
  代入全部原始约束、核向量、证书、整除失败（含 Bareiss 精确行列式 `det(U)=±1`）、矛盾环。
- **每步变换说明适用域**：所有类型运算与消元变换都记录 `Step(action, domain, detail)`。

## 目录结构

```
unitchk/
  core.py         指数向量、比例/仿射单位、类型运算规则
  constraints.py  表达式 → 整数线性系统 A x = b
  solver.py       整数精确求解（解格 / 不相容证据）
  graph.py        版本化转换图（仿射映射、矛盾环）
  verify.py       独立代入验证
  steps.py        变换步骤（含适用域）
tests/            unittest 测试（47 个）
demo.py           运行样例
```

## 快速上手

```python
from unitchk import *

reg = Registry(["L", "M", "T", "Θ"])
reg.define("m", {"L": 1}); reg.define("s", {"T": 1}); reg.define("kg", {"M": 1})
reg.define("mps", {"L": 1, "T": -1}); reg.define("mpss", {"L": 1, "T": -2})

# 1) 速度 × 时间 = 长度
log = StepLog()
check_equation(Equation(Mul(Leaf("mps"), Leaf("s")), Leaf("m"), "v*t=s"), reg, log)

# 2) 未知单位求解：F = kg · m/s² → F = L·M·T^-2
system = build_system([Equation(Leaf("F"), Mul(Leaf("kg"), Leaf("mpss")), "F=ma")],
                      reg, {"F"})
sol = solve_integer_system(system.A, system.b, system.var_names, system.row_labels)
print(solution_unit_vectors(system, sol))   # {'F': ((1, 1, -2, 0), [])}

# 3) 绝对仿射温度不参与普通乘法（抛 UnitTypeError）；degC - degC 的差量可参与
```

## 运行

```bash
python3 demo.py                          # 10 个场景的运行样例
python3 -m unittest discover -s tests    # 47 个测试
```

## 运行样例（节选）

```
1. 速度 × 时间 = 长度（已知单位联合类型检查）
  mps * s 的类型 = L，与 m 一致 ✓
    步骤: [mul] 比例量相乘：整数指数向量逐维相加

3. 未知单位约束求解：F = kg · m/s²
  解得 F 的指数向量 = (1, 1, -2, 0) 即 L·M·T^-2（唯一解）
  独立代入全部原始约束: 全部成立 ✓

4. 欠定方程：X · Y = m → 整数解格与自由维度
  格维数 = 4，自由变量 = ['Y[L]', 'Y[M]', 'Y[T]', 'Y[Θ]']

5. 矛盾方程组：X·s = m 与 X·s = kg → 不相容证书
  方程 eq2[L] 残差 -1；整数证书 c 满足 c·A = 0 且 c·b = -1 ≠ 0
  独立验证证书 c·A=0 且 c·b≠0: 成立 ✓

6. 整数整除性：X² = m → 2·X[L] = 1 在 Z 上无解
  类型: divisibility；方程 X^2=m[L] 要求 2 | 1，整数整除失败

7. 版本化转换图
  mile -> km: 因子 25146/15625（精确有理数）
  声明 1 mile = 1.6 km -> 拒绝：环上复合映射 12500/12573 不是恒等映射
  历史版本 v5 快照 find(mile,km) 因子 = 25146/15625 ✓

8. 仿射温度换算（比例与偏移分开）
  degF -> degC: x -> (5/9)*x + (-160/9)；32°F = 0°C，212°F = 100°C（精确）
```

## 测试结果

```
$ python3 -m unittest discover -s tests
Ran 47 tests in 0.005s
OK
```

覆盖：速度×时间=长度、绝对温度乘/除/幂拒绝、差量参与运算、未知单位自由维度、
唯一解/解格/自由变量、矛盾证书与整除证据（含篡改证据被拒绝）、精确有理数换算、
矛盾环拒绝与独立重算、版本快照查询、仿射温度换算、代入/核/证书/环的独立验证。
