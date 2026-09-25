"""运行样例：python3.11 example.py

用重启 GMRES 求解一维 Poisson 方程，并与自写稠密高斯消元互相核验。
"""

from __future__ import annotations

import math

from dense import DenseMatrix, dense_solve
from gmres import gmres


def poisson_matrix(n: int) -> DenseMatrix:
    rows = [[0.0] * n for _ in range(n)]
    for i in range(n):
        rows[i][i] = 2.0
        if i > 0:
            rows[i][i - 1] = -1.0
        if i + 1 < n:
            rows[i][i + 1] = -1.0
    return DenseMatrix(rows)


def main() -> None:
    n = 40
    a = poisson_matrix(n)
    x_true = [math.sin(2.0 * math.pi * i / (n - 1)) for i in range(n)]
    b = a.matvec(x_true, n)
    res = gmres(a.matvec, b, n, restart=10, tol=1e-10, max_restarts=50)
    print(f"尺寸 n            = {n}")
    print(f"重启长度 restart  = 10")
    print(f"收敛              = {res.converged} (原因: {res.reason})")
    print(f"重启周期数        = {res.restart_cycles}")
    print(f"Arnoldi 总步数    = {res.arnoldi_steps}")
    print(f"matvec 调用次数   = {res.matvec_count}")
    print(f"最终残差范数      = {res.residual_norm:.3e}")
    print("重启边界真实残差  = "
          + ", ".join(f"{v:.2e}" for v in res.restart_residuals))
    print("前 5 个 Arnoldi 残差估计 = "
          + ", ".join(f"{v:.2e}" for v in res.residual_history[:5]))
    x_ref = dense_solve(a.rows, b)
    err_dense = max(abs(xi - ri) for xi, ri in zip(res.x, x_ref))
    err_true = max(abs(xi - ti) for xi, ti in zip(res.x, x_true))
    print(f"与稠密解最大偏差  = {err_dense:.3e}")
    print(f"与真解最大偏差    = {err_true:.3e}")


if __name__ == "__main__":
    main()
