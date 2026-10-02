# sparse-cg

纯 Python 3.11（仅标准库）实现的稀疏预条件共轭梯度（PCG）求解器，
不依赖 NumPy/SciPy。强调**只信任显式重算的真实残差**。

## 布局

- `sparse_cg/matrix.py` — CSR 稀疏矩阵 `SparseMatrix`：尺寸与索引全部显式输入
  （`n_rows, n_cols, indptr, indices, values`），带单调 `version` 计数，
  任何就地修改（`update_value`）都会递增版本。
- `sparse_cg/preconditioner.py` — 可更新对角（Jacobi）预条件器，
  矩阵变更后可 `update(...)` / `update_from_matrix(...)` 热更新。
- `sparse_cg/solver.py` — `CGSolver`：热启动、周期真实残差重算、停滞检测、
  checkpoint/恢复、取消。
- `sparse_cg/dense.py` — 自写稠密高斯消元（列主元），仅用于小规模核验。
- `tests/test_sparse_cg.py` — unittest 套件（17 个用例）。

## 收敛口径（固定语义）

记 `r_true = b - A x`，`bnorm = ||b||_2`。收敛**当且仅当**

```
||r_true||_2 <= max(atol, rtol * bnorm)
```

- 只有显式重算的真实残差能宣布收敛；递推残差只作为"何时花一次 matvec
  重算真实残差"的廉价提示。
- 每 `recompute_interval` 次迭代强制重算一次真实残差；递推残差声称
  收敛时也会立即重算。此类"假收敛"事件记录在
  `result.premature_convergence_events`。
- 重算真实残差后做软重启（`p = z`），因为旧搜索方向对新残差不再满足
  A-共轭。
- 停滞判定只看真实残差历史：连续 `stagnation_window` 个重算点未能把范数
  降低 `stagnation_min_reduction` 倍 → `STAGNATED`，附范数证据。

## 状态与数值证据

`CGResult.status ∈ {CONVERGED, MAX_ITERATIONS, STAGNATED,
NOT_POSITIVE_DEFINITE, ASYMMETRIC_INPUT, INVALID_PRECONDITIONER,
MATVEC_FAILURE, CANCELLED}`，非收敛状态都在 `result.evidence` 中附具体
数值证据（如 `pAp` 值与迭代号、非对称元 `(i, j, a_ij, a_ji, defect)`、
零对角元下标、matvec 异常与迭代号、停滞前后的范数）。
`MAX_ITERATIONS` 明确表示"预算耗尽"，**不**被当作"无解"。

## 热启动与 checkpoint

- 每次 `solve()` 都从 `x0` 完整重建 Krylov 状态（`r, z, p`），复用的只有
  `x` 本身；矩阵 `version` 与上次求解不一致时 `result.hot_start=True`。
- `save_checkpoint()` 只保存 `x` 与标量（迭代号、矩阵版本、真实残差范数、
  `||b||`），从不保存搜索方向。
- `restore_checkpoint(A, ckpt)` 在矩阵版本不一致时抛
  `VersionMismatchError`（附新旧版本号证据），拒绝误用旧算子下的状态；
  显式 `allow_matrix_change=True` 才允许仅热启动 `x`。

## 内存

工作内存为固定数量的 O(n) 向量（`x, r, z, p, Ap`），不存储 Krylov 基，
不随迭代次数增长。

## 运行测试

```
python3.11 -m unittest discover -s tests -v
```

实际运行结果见 `test_results.txt`。
