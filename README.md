# sparse-qr-ls

纯 Python 3.11 标准库实现的列主元 QR 最小二乘求解器，面向小中型稀疏矩阵。
不使用 NumPy/SciPy，不构造法方程，不求显式逆。

## 特性

- 矩阵以显式尺寸 + 显式 `(row, col, value)` 三元组输入（`SparseMatrix`）
- 列主元 Householder QR：每步选尾范数最大的剩余列，抑制近相关列带来的数值问题
- 秩估计：主元小于容差（默认 `max(m,n) * eps * 最大列范数`）即判秩亏
- 重复/近相关列导致秩亏时：`rank_deficient=True`，并报告 `dependent_columns`（原始列号）
- 过定系统返回最小二乘解与残差向量 `b - A x` 及其范数
- 列主元置换在内部进行，解 `x` 始终按**原始变量顺序**返回
- 回代解三角系统，绝不显式求逆

## 用法

```python
from sparse_qr import SparseMatrix, qr_least_squares

A = SparseMatrix(5, 3, [(0,0,1),(1,0,1),(1,1,1),(2,0,1),(2,1,2),(2,2,4),
                        (3,0,1),(3,1,3),(3,2,9),(4,0,1),(4,1,4),(4,2,16)])
res = qr_least_squares(A, [1.0, 2.0, 0.0, 1.0, 5.0])
res.x, res.rank, res.residual_norm, res.rank_deficient
```

## 文件

- `sparse_qr.py` — 求解器（含 `python3.11 sparse_qr.py` 演示）
- `dense_reference.py` — 独立编写的稠密参考实现，用于小规模核验
- `test_sparse_qr.py` — unittest 测试（12 个用例）

## 运行样例（`python3.11 sparse_qr.py`）

```
=== Example 1: overdetermined 5x3 least squares (quadratic fit) ===
x      = [1.6857142857, -1.8714285714, 0.6428571429]
rank   = 3  rank_deficient = False
||r||  = 2.02837021134844

=== Example 2: duplicate columns -> rank deficiency reported ===
x      = [0.2, 0.2, 0.0]
rank   = 2  rank_deficient = True
dependent columns = [2]
||r||  = 0.0

=== Example 3: column pivoting, solution in original order ===
pivot order (original indices) = [1, 2, 0]
x (original order) = [10000.0, -0.0, 2.0]
A @ x - b = ['-1.11e-16', '0.00e+00', '0.00e+00']
```

## 测试结果（`python3.11 -m unittest test_sparse_qr -v`）

```
Ran 12 tests in 0.004s
OK
```

覆盖：过定精确/含噪系统、残差与列空间正交性、重复列与三重重复列秩亏报告、
近相关列秩估计、零矩阵、列主元后原变量顺序还原、行置换不变性、
欠定系统、30 组随机问题与稠密参考实现对照、输入校验。
