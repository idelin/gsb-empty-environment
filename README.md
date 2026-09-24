# sparseqr — 稀疏 QR 最小二乘（纯标准库，Python 3.11）

适用于小中型稀疏矩阵的最小二乘求解器：基于带列主元的 Householder QR，
给出秩估计、残差与原变量顺序的解。不构造正规方程、不做任何矩阵求逆，
不依赖 NumPy/SciPy。

## 特性

- 稀疏输入：显式给定行数、列数与 `(行, 列, 值)` 三元组（重复项自动求和）。
- 列主元 QR：每步选取尾部范数最大的列，抑制近相关列带来的不稳定。
- 秩估计：按 `|R[k][k]|` 阈值（默认 `max(m,n) * eps * max|R diag|`，可用
  `tol=` 覆盖）判定数值秩；秩亏时报告 `dependent_columns`（原始列号）。
- 最小二乘解：超定系统返回极小残差解；秩亏时取基本解（冗余列系数为 0），
 避免近相关方向系数爆炸。
- 顺序还原：内部列置换 `AP = QR`，返回的 `x` 始终为原始变量顺序。

## 用法

```python
from sparseqr import SparseMatrix, qr_least_squares

# 4x3，显式尺寸 + 显式索引；第 2 列与第 0 列重复 -> 秩亏
a = SparseMatrix(4, 3, [
    (0, 0, 1.0), (1, 0, 2.0), (2, 0, 3.0), (3, 0, 4.0),
    (0, 1, 2.0), (1, 1, 0.0), (2, 1, 1.0), (3, 1, -1.0),
    (0, 2, 1.0), (1, 2, 2.0), (2, 2, 3.0), (3, 2, 4.0),
])
res = qr_least_squares(a, [1.0, 2.0, 3.0, 4.0])
res.x                  # [1.0, 0.0, 0.0] —— 原始变量顺序
res.rank               # 2
res.rank_deficient     # True
res.dependent_columns  # [2]
res.residual_norm      # 0.0
res.column_permutation # 内部主元顺序（AP = QR 的 P）
```

## 运行样例

```
python3.11 examples/demo.py
```

输出（节选）：

```
--- Overdetermined line fit (8 points, 2 unknowns) ---
  x (original order) : [1.083333, 1.97619]
  rank               : 2
  residual norm      : 7.590721e-01

--- Duplicate column (col 2 == col 0) ---
  x (original order) : [1.0, 0.0, 0.0]
  rank               : 2
  rank deficient     : True
  dependent columns  : [2]

--- Column pivoting with order restoration ---
  x (original order) : [2000.0, -1.0, 3.0]
  column permutation : [1, 2, 0]
  check A x          : [1.0, 2.0, 3.0] (b = [1.0, 2.0, 3.0])

--- Sparse random 200x60 (nnz=471), consistent rhs ---
  rank               : 60
  residual norm      : 4.033730e-15
```

## 测试

```
python3.11 -m unittest discover -s tests -v
```

结果：`Ran 16 tests ... OK`。测试覆盖：显式尺寸/索引输入校验、超定最小二乘、
重复列/比例列秩亏报告、近相关列的列主元稳定性、行列重排后的变量顺序还原、
欠定基本解、零矩阵/零列边界，以及与自写稠密 MGS-QR 参考实现
（`sparseqr/dense.py`）的随机交叉核验（满秩与秩亏各 25 组）。

## 文件结构

- `sparseqr/sparse_matrix.py` — 稀疏矩阵（列字典存储，显式索引构造）
- `sparseqr/qr.py` — 带列主元的 Householder QR 最小二乘（主求解器）
- `sparseqr/dense.py` — 自写稠密 MGS-QR 参考实现（仅用于核验）
- `tests/test_qr.py` — unittest 测试套件
- `examples/demo.py` — 运行样例
