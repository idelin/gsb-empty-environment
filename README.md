# sparse_cg

纯标准库（Python 3.11，无 NumPy/SciPy）的稀疏共轭梯度（CG）求解器，带可更新
对角（Jacobi）预条件，支持矩阵版本变更后的热启动、周期性真实残差重算、停滞
检测与 checkpoint 恢复。

## 固定口径

- **残差**：`r = b - A x` 的 2-范数。
- **容差**：收敛当且仅当 `||b - A x||_2 <= max(atol, rtol * ||b||_2)`。
- **真实残差优先**：只有显式重算的真实残差可以宣布收敛。递推残差低于容差时
  仅触发一次真实残差重算；真实残差每 `true_residual_period` 次迭代周期性重算，
  并驱动收敛与停滞判定。递推关系本身不被改写（把真实残差回灌进递推会在病态
  问题上破坏 CG 递推稳定性）。
- **工作内存**：O(n) —— 固定 5 个工作向量 `x, r, p, z, Ap` 加右端项，不存储
  Krylov 基，向量内存不随迭代数增长（仅保留标量范数历史）。

## 尺寸与索引显式输入

```python
from sparse_cg import SparseMatrix, CGSolver, DiagonalPreconditioner

A = SparseMatrix(n_rows=3, n_cols=3,
                 rows=[0, 0, 1, 1, 2], cols=[0, 1, 0, 1, 2],
                 values=[2.0, -1.0, -1.0, 2.0, 4.0], version=1)
pre = DiagonalPreconditioner(3).update_from_matrix(A)   # 矩阵变更后可再次 update
solver = CGSolver(A, preconditioner=pre, rtol=1e-8, atol=1e-12,
                  true_residual_period=50)
result = solver.solve([1.0, 0.0, 1.0])
```

## 状态与数值证据

`result.status` ∈ `converged | max_iter | stagnated | indefinite |
indefinite_preconditioner | asymmetric | cancelled`。未收敛（如 `max_iter`）
返回当前最优迭代解，不会被当作"无解"异常。非对称输入与正定性丧失均通过
`result.evidence` 返回具体数值证据：

- `asymmetric`: `max_abs_asymmetry`, `(row, col)`, `value_ij`, `value_ji`
- `indefinite`: `pAp <= 0` 的值、迭代号、`||p||`
- `indefinite_preconditioner`: `rz <= 0` 的值
- `stagnated`: 真实残差 checkpoint 序列、窗口与衰减因子

## 热启动、checkpoint 与版本

- 矩阵变更后热启动：`solver.solve(b, x0=old_x)` —— `solve` 总是从当前矩阵
  重建 Krylov 状态（重算 `r = b - A x0`、重置搜索方向），旧解仅作为初值复用。
- `solver.checkpoint()` 生成 JSON 可序列化的 O(n) 快照（含 `matrix_version`）；
  `CGSolver.restore(ckpt, matrix)` 在矩阵版本不一致时抛出 `MatrixVersionError`，
  拒绝把旧搜索方向误用于变更后的矩阵。
- 取消：`iteration_callback` 返回 `False` 或 `solver.request_cancel()`；状态在
  每次迭代边界提交，`solver.current_x`/`checkpoint()` 在 matvec 异常或取消后
  仍可用于重启（`solver.resume(max_iter)`）。

## 测试

```sh
python3.11 -m unittest discover -v
```

小规模正确性由 `sparse_cg/dense.py` 中自写的高斯消元（部分主元）核验。
实际运行结果见 `RESULTS.md`。
