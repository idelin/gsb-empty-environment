"""Demo: restarted GMRES on a matrix-free 1D Poisson problem.

Run:  python3 example.py
"""

from gmres import gmres


def make_poisson_matvec(n):
    """Matrix-free matvec for the 1D Poisson operator [-1, 2, -1]."""

    def matvec(x):
        return [
            2.0 * x[i]
            - (x[i - 1] if i > 0 else 0.0)
            - (x[i + 1] if i < n - 1 else 0.0)
            for i in range(n)
        ]

    return matvec


def show(title, res):
    print(f"--- {title} ---")
    print(f"converged        : {res.converged}  (reason: {res.reason})")
    print(f"iterations       : {res.iterations} Arnoldi steps, "
          f"{res.restart_cycles} restart cycles, {res.matvec_calls} matvecs")
    print(f"peak memory      : {res.peak_krylov_vectors} Krylov vectors")
    print(f"true residual    : {res.true_residual}")
    hist = res.residual_history
    shown = hist if len(hist) <= 8 else hist[:4] + ["..."] + hist[-4:]
    print(f"Arnoldi residuals: {shown}")
    print(f"cycle residuals  : {res.cycle_residuals}")
    print()


n = 64
b = [1.0] * n

# 1) normal solve, restart length bounds working memory to 21 vectors
res = gmres(make_poisson_matvec(n), b, n=n, restart=30, tol=1e-9, maxiter=500)
show("Poisson n=64, restart=30, tol=1e-9", res)

# 2) zero right-hand side: returns immediately, no matvec calls
res0 = gmres(make_poisson_matvec(n), [0.0] * n, n=n)
show("zero right-hand side", res0)

# 3) tiny budget: honestly reports non-convergence
res_budget = gmres(make_poisson_matvec(n), b, n=n, restart=10, maxiter=3)
show("budget exhausted (maxiter=3)", res_budget)
