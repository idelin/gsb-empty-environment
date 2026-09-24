# 增量加权最小二乘（秩一 Cholesky 更新/降阶）

纯 Python 3.11 标准库实现（不使用 NumPy/SciPy）。逐行加入或撤回观测，
维护正规方程

```
M x = z,   M = Aᵀ W A + λI,   z = Aᵀ W b
```

的 Cholesky 因子，通过**秩一更新（update）/降阶（downdate）**增量修改，
而不是每次完整重解；仅在秩变迁或周期复核发现漂移时才做 O(n³) 全量重构。

## 文件结构

- `ils_linalg.py` — 稠密原语：Cholesky 分解、秩一 update/downdate、三角回代、RREF（解空间/秩）
- `ils_solver.py` — `IncrementalLeastSquares` 主类（增删、回滚、求解、周期复核）
- `ils_reference.py` — 独立高精度参考：`fractions.Fraction` 精确构造并求解正规方程
- `test_ils.py` — unittest 套件（18 个测试）
- `demo.py` — 运行样例

## 设计要点

- **显式尺寸与索引**：`IncrementalLeastSquares(n)` 声明维数；每个样本带显式 `sample_id`，
  重复 id 拒绝（`DuplicateSampleError`），撤回不存在的 id 报 `UnknownSampleError`。
- **权重与正则化**：`weight >= 0`，`regularization = λ >= 0`。
  - 零权样本：id 有定义（可查询、可撤回），但对 M 和 z 无贡献；
  - 负权拒绝（`InvalidWeightError`），保证不破坏 Cholesky 依赖的正定假设。
- **原子回滚**：所有校验先于任何变更；因子在副本上试更新，失败（数值降秩、溢出）
  时旧因子、右端项与样本表完全不变，抛 `UpdateError`（`stats["rollbacks"]` 计数）。
- **秩下降处理**：撤回导致 M 秩 < n 时，撤回生效并切换到稠密回退路径，
  `solve()` 返回 `status="deficient"` 的解空间（特解 + 零空间基，
  全部解为 `x + span(null_space)`）；λ > 0 时 M 始终正定，仍返回唯一正则化解。
  之后加入观测若恢复满秩，自动全量重构并回到因子路径。
- **周期复核**：每 `verify_interval` 次更新（或手动 `verify()`）从原始观测重建 M、z，
  核对因子偏差、右端项偏差与解的残差；超差则自愈式重构。
- **高精度参考核验**：测试与样例用 `Fraction` 精确算术独立求解同一问题，
  报告最大相对误差（float 输入按其二进制精确值转换，隔离算法舍入误差）。

## 用法

```python
from ils_solver import IncrementalLeastSquares

solver = IncrementalLeastSquares(3, regularization=1e-8, verify_interval=64)
solver.add("s1", [1.0, 0.5, -0.25], 2.0, weight=1.0)
solver.add("s2", [0.0, 1.0, 0.75], -1.0, weight=2.0)
sol = solver.solve()            # Solution(status="unique", x=[...], ...)
solver.retract("s1")            # 加权滑窗：由调用方维护窗口，逐条增删
report = solver.verify()        # 与原始观测复核：因子/右端项/残差
```

## 运行样例（`python3.11 demo.py`）

```
======================================================================
1. Incremental weighted solve (rank-one Cholesky updates)
======================================================================
added   d1   weight=1.0
added   d2   weight=2.0
added   d3   weight=0.5
added   d4   weight=1.0
added   d5   weight=1.5
solution: [0.9160839136, -0.2167832158, -1.005371505e-09]
status  : unique (rank 3), rel. error vs exact reference: 2.043e-16
verify  : ok=True factor_dev=2.41e-16 rhs_dev=0.00e+00 residual=2.41e-16

======================================================================
2. Weighted sliding window (add / retract, window = 4)
======================================================================
add w1                   -> x = [0.999999, 0]
add w2                   -> x = [0.999999, 1.999998]
add w3                   -> x = [1.2000002, 2.1999992]
add w4                   -> x = [1.0588239, 2.2352932]
add w5  / retract w1     -> x = [1.1351351, 2.1621617]
add w6  / retract w2     -> x = [1.1025642, 2.2051276]
add w7  / retract w3     -> x = [0.99999994, 1.9999997]
final verify: ok=True residual=1.24e-15 (checked against raw observations)

======================================================================
3. Retraction causing a rank drop -> solution space
======================================================================
full rank     : [3, 4]
after retract : status=deficient rank=1
particular    : [3, 0]
null space    : ['[-0, 1]']
=> all solutions: x = [3, 0] + t * [-0, 1]
regularized   : status=unique x=[2.9997, 0] (unique)

======================================================================
4. Validation and atomic rollback
======================================================================
retract unknown id -> UnknownSampleError: sample id 'ghost' is not registered
negative weight    -> InvalidWeightError: negative weights are rejected: they would break the positive-definiteness assumption
zero-weight sample registered: True, matrix unchanged: True
failed downdate    -> UpdateError: downdate failed although the system stays full rank; state rolled back
state intact after rollback: True (rollbacks=1)

======================================================================
5. Numerical stress report (vs exact rational reference)
======================================================================
scenario                               max relative error
----------------------------------------------------------
large offsets (b ~ 1e6)                2.444e-15
near-correlated columns (1e-6)         4.297e-10
tiny weights (1e-12) + downdate        1.110e-16
```

## 测试结果（`python3.11 -m unittest test_ils -v`）

```
Ran 18 tests in 0.012s
OK

[report] large-offset targets relative error: 2.925e-14
[report] near-correlated columns relative error: 4.297e-10
[report] tiny-weight relative error: 3.331e-16
[report] sliding-window max relative error: 9.235e-10
```

覆盖：精确参考一致性、加入再撤回恢复矩阵、逆序撤回、滑窗增删、
秩下降解空间、正则化唯一解、零权/负权/重复 id/未知 id/维度/非有限值校验、
降阶失败原子回滚、周期复核与自愈、大偏移/近相关列/极小权重数值压力。

## 限制

- 秩一 downdate 在极端病态下会损失精度；`rank_tol` 控制降秩判定阈值，
  周期复核与全量重构用于抑制长期漂移。
- 秩变迁时退化为 O(n³) 稠密路径（RREF），这是正确性优先的设计选择。
- 稠密实现面向中小规模问题；未做稀疏/分块优化。
