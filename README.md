# dualtag

从零实现的**可嵌套、带标签的双数自动微分 + 纯表达式解释器**，仅使用
Python 标准库（兼容 3.11+），不依赖任何自动微分或张量库。

## 特性

- **标签隔离的嵌套微分**：每次求导创建全新 `Tag`，按标签精确提取切量，
  内层求导引用外层变量时不会发生扰动混淆（perturbation confusion）。
- **多方向 JVP**：一次前向传播同时携带多个方向的种子。
- **二阶与混合导数**：`second_derivative`、`mixed_directional`（uᵀHv）、
  `mixed_partial`、`hessian`，通过标签嵌套（切量的切量）实现。
- **可微隐式求根**：标量与方程组 Newton 求解 + 隐函数定理求导；
  显式报告收敛性与雅可比奇异性，**未收敛或奇异时拒绝给出导数**。
- **非光滑点显式标记**：`abs`/`min`/`max` 的折点、分支条件恰在边界时
  抛出 `NonDifferentiableError`。
- **步数与内存预算**：`Budget` 上下文管理器，超限时抛出 `BudgetExceeded`；
  取消/恢复不串标签状态（`active_tags()` 可验证）。
- **自实现小张量**：`dualtag/tensor.py` 提供 matmul/matvec/带行平衡与
  部分主元的高斯消元，奇异矩阵显式报错。
- **独立验证**：解析结果、中心有限差分、小型符号求导器三方对照。

## 目录结构

```
dualtag/
  dual.py         标签双数（primal 可再嵌套 Dual，多方向切量）
  tags.py         扰动标签与作用域（防泄漏）
  diff.py         jvp / derivative / gradient / jacobian / 二阶与混合导数
  interpreter.py  基于 ast 的纯表达式解释器（含非光滑检测）
  implicit.py     Newton 求根 + 隐函数定理求导（标量与方程组）
  symbolic.py     小型符号求导器（独立交叉验证用）
  tensor.py       纯 Python 小矩阵运算与线性求解
  budget.py       步数/内存预算
  errors.py       异常体系
tests/            unittest 测试（64 个）
examples/demo.py  可运行样例
```

## 快速上手

```python
from dualtag import derivative, second_derivative, mixed_directional

derivative(lambda x: x**3, 2.0)                 # 12.0  (= 3x²)
second_derivative(lambda x: x**3, 2.0)          # 12.0  (= 6x)
mixed_directional(lambda x: x**3, 2.0, 3.0, -0.5)  # -18.0 (= 6x·u·v)

# 嵌套求导无扰动混淆：结果是 1.0，混淆实现会得 2
from dualtag import derivative as d
d(lambda x: x * d(lambda y: x + y, 5.0), 3.0)   # 1.0

# 隐式求导：y² = x, y > 0  ⇒  dy/dx = 1/(2√x)
from dualtag import implicit_derivative
implicit_derivative(lambda x, y: y*y - x, 4.0, y0=1.5)   # 0.25
# x = 0 处雅可比奇异 → 抛出 SingularJacobianError（明确失败）

# 表达式解释器
from dualtag import compile_expression
f = compile_expression("x**3 - 2*sin(x)", ["x"])
derivative(f, 0.7)

# 预算
from dualtag import Budget, jvp
with Budget(max_steps=100, max_memory=64):
    jvp(lambda x: x**3, 2.0, (1.0, 2.0))
```

## 设计要点

- **防扰动混淆**：导数因子（链式法则系数）在"剔除当前 tag、保留其余
  tag"的操作数上求值（见 `dual.py:_strip_tag`）。这保证每个 tag 都是
  独立的一阶幂零方向，同时让嵌套/混合导数的交叉项正确传播。
- **奇异性判定**：标量情形默认阈值 `max(1e-12, √tol)·(1+|F_y⁰|)`——
  重根处 Newton 仅线性收敛，收敛点的 |F_y| 为 O(√tol)，可被可靠识别；
  方程组情形经行平衡后主元阈值取 `max(1e-12, √tol)`。均可用
  `sing_tol=` 覆盖。
- **不给假导数**：`implicit_derivative` / `implicit_jacobian` 先检查
  `RootResult.singular` 与 `converged`，分别抛出
  `SingularJacobianError` / `NotConvergedError`。

## 运行

```bash
python3 examples/demo.py            # 可运行样例
python3 -m unittest discover -s tests -t .   # 测试（或 -v 查看明细）
```

## 测试结果

```
Ran 64 tests in 0.007s
OK
```

覆盖：x³ 的一阶/二阶/混合方向解析验证、嵌套求导防混淆（含三层嵌套）、
多方向 JVP、梯度/雅可比/黑塞矩阵、有限差分对照、符号求导对照、
y²=x 隐式求导（含 x=0 奇异失败、未收敛拒绝、方程组与有限差分对照）、
非光滑标记（abs/min/max/分支边界）、步数与内存预算及取消后标签状态恢复、
张量运算与奇异矩阵检测。
