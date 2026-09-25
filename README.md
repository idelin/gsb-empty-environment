# 重启 GMRES（纯标准库，Python 3.11）

不依赖 NumPy/SciPy 的重启 GMRES(m) 实现，矩阵仅通过回调访问。

## 文件

- `gmres.py` — 重启 GMRES 求解器（Arnoldi + Givens 旋转）。
- `dense.py` — 自写稠密矩阵与高斯消元，用于小规模核验。
- `test_gmres.py` — unittest 测试套件。
- `example.py` — 运行样例（Poisson 方程，与稠密解互验）。

## 接口

```python
from gmres import gmres

res = gmres(matvec, b, n, x0=None, restart=20, tol=1e-8,
            max_restarts=100, max_matvec=None, stagnation_rtol=1e-10)
```

- `matvec(x, n) -> y`：矩阵向量回调，尺寸 `n` 显式传入。
- `b`：右端，长度必须等于显式尺寸 `n`；`x0` 同理。
- 返回 `GmresResult`：`x`、`converged`、`reason`、`restart_cycles`、
  `arnoldi_steps`、`matvec_count`、`residual_norm`、
  `residual_history`（每个 Arnoldi 步的残差估计）、
  `restart_residuals`（每个重启边界的真实残差）。

## 行为约定

- 工作内存 O(n * restart + restart^2)：Arnoldi 基至多 `restart + 1` 个
  向量，Hessenberg 矩阵 `(restart + 1) x restart`，重启间不保留。
- 零右端立即返回（不调用 matvec），`reason="zero_rhs"`。
- 停止原因：`converged` / `zero_rhs` / `stagnation` / `budget_exhausted`；
  达到预算（`max_restarts` 或 `max_matvec`）时 `converged=False`，
  绝不伪称收敛。
- matvec 返回尺寸错误、非有限值或回调抛异常时，立即抛出
  `MatvecError`，消息指明重启周期 / Arnoldi 步与分量下标。
- 输入 `b` / `x0` 尺寸不符或含非有限值时抛 `ValueError` 并指明下标。

## 运行

```sh
python3.11 example.py        # 运行样例
python3.11 -m unittest -v    # 运行测试
```
