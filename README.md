# dualtag

从零实现的标签化对偶数微分与纯表达式解释器。纯 Python 3.11 标准库，
不依赖任何自动微分或张量库。

## 功能

- **嵌套微分与扰动隔离**：每次求导分配全局唯一、永不复用的 `Tag`；
  不同标签的值相遇时在较新（内层）标签中运算，外层扰动完整保留在
  primal 内——内层求导引用外层变量不会发生扰动混淆。
- **多方向 JVP**：一个标签携带多个方向种子，单次前向传播得到整批
  `J @ v` 列；另有 `jacobian` / `gradient` / `hessian` / `mixed_partial`
  / `second_derivative`（二阶混合导数用两个嵌套标签取交叉项系数）。
- **可微隐式求根**：`solve_implicit`（牛顿法，报告收敛性、迭代数、
  残差、雅可比奇异性）+ `implicit_jvp`（隐函数定理 `dy/dx = -Jy⁻¹ Fx`）。
  未收敛抛 `NonConvergenceError`，雅可比奇异抛 `SingularJacobianError`
  （枢轴检验 + 线性收敛速率检测双重判定），绝不对未收敛近似值或奇异
  点给出假精确导数。非光滑分支（`abs`/`min`/`max`/`sqrt@0`）抛
  `NonDifferentiableError`。
- **资源预算**：`Budget(max_steps, max_allocs)` 限制算术步数与对偶数
  分配；`budget_scope` 在异常（取消）时保证恢复，标签计数单调不复用，
  取消后状态不串。
- **小规模张量**：`Tensor`（elementwise / matmul / norm）与部分主元
  高斯消元 `solve`，奇异时显式报错。
- **独立数值验证**：`numeric`（中心差分、四点混合差分、二分求根）与
  `symbolic`（AST 符号求导 + 化简）作为交叉对照。

## 布局

```
dualtag/
  dual.py      标签 + 对偶数算术 + 初等函数（扰动隔离核心）
  ad.py        derivative / jvp / jacobian / hessian / mixed_partial
  implicit.py  牛顿求根 + 隐函数定理求导（收敛/奇异报告）
  expr.py      纯表达式解释器（+ - * / ^、函数调用、变量）
  symbolic.py  符号求导（独立对照）
  numeric.py   有限差分 + 二分求根（独立验证）
  tensor.py    小张量与线性求解
  budget.py    步数/内存预算与异常安全作用域
  errors.py    错误类型
examples.py    可运行样例：python3 examples.py
tests/         unittest 套件
```

## 运行

```bash
python3 examples.py                          # 样例演示
python3 -m unittest discover -v              # 测试（47 项）
```

## 快速示例

```python
import dualtag as dt

dt.second_derivative(lambda x: x**3, 2.0)        # (8.0, 12.0, 12.0)
dt.mixed_partial(lambda v: v[0]**3 * v[1] + v[0]*dt.sin(v[1]),
                 [1.5, 0.7], 0, 1)               # 3x^2 + cos(y)

# 嵌套：d/dx [ x * d/dy (x*y) ] = 2x，内外扰动不混淆
def outer(x):
    _, d = dt.derivative(lambda y: x * y, 1.0)
    return x * d
dt.derivative(outer, 3.0)                        # (..., 6.0)

# 隐式：y^2 = x, y > 0
res = lambda p, y: [y[0]**2 - p[0]]
dydx, result = dt.implicit_jvp(res, [4.0], [1.0], [[1.0]])
# dydx = [[0.25]]；x=0 时抛 SingularJacobianError
```
