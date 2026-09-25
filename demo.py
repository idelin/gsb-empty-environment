"""Demo: rank-1 / low-rank updates on a factorized system (pure stdlib)."""

from lowrank import (
    LU,
    LowRankUpdate,
    RankOneUpdate,
    SingularUpdateError,
    dense_from_sparse,
    matvec,
)


def show(title):
    print(f"\n=== {title} ===")


def residual(a, x, b):
    return max(abs(ai - bi) for ai, bi in zip(matvec(a, x), b))


n = 4
A = [
    [4.0, 1.0, 0.5, 0.0],
    [1.0, 3.0, 0.5, 0.2],
    [0.5, 0.5, 5.0, 1.0],
    [0.0, 0.2, 1.0, 3.5],
]
b = [1.0, 2.0, 3.0, 4.0]

# One factorization, reused by every update below.
lu = LU(A, n)
x0 = lu.solve(b)
show("base factorization")
print("x0 =", [round(v, 6) for v in x0])

# --- rank-1 update: A + u v^T -------------------------------------------
u_idx, u_val = [0, 2], [1.0, -0.5]
v_idx, v_val = [1, 3], [0.5, 2.0]
upd = RankOneUpdate(lu, u_idx, u_val, v_idx, v_val, n)
x1 = upd.solve(b)
show("rank-1 update (Sherman-Morrison)")
print("denominator 1 + v^T A^-1 u =", round(upd.denominator, 6))
print("x1 =", [round(v, 6) for v in x1])

u = dense_from_sparse(n, u_idx, u_val)
v = dense_from_sparse(n, v_idx, v_val)
A1 = [[A[i][j] + u[i] * v[j] for j in range(n)] for i in range(n)]
x_ref = LU(A1, n).solve(b)  # direct reference: full refactorization
print("direct reference       =", [round(vv, 6) for vv in x_ref])
print("max |x1 - x_ref|       =", f"{max(abs(p - q) for p, q in zip(x1, x_ref)):.2e}")

# --- multiple right-hand sides -------------------------------------------
rhs = [b, [4.0, 3.0, 2.0, 1.0], [0.0, 1.0, 0.0, -1.0]]
sol_upd = upd.solve_many(rhs)
sol_ref = LU(A1, n).solve_many(rhs)
err = max(abs(p - q) for xs, ys in zip(sol_upd, sol_ref)
          for p, q in zip(xs, ys))
show("multiple right-hand sides")
print(f"{len(rhs)} RHS, max error vs direct reference = {err:.2e}")

# --- reverse update recovers the original solution ------------------------
back = upd.reverse_update()
x_back = back.solve(b)
show("reverse update (A + u v^T) - u v^T = A")
print("x_back =", [round(vv, 6) for vv in x_back])
print("max |x_back - x0| =", f"{max(abs(p - q) for p, q in zip(x_back, x0)):.2e}")

# --- low-rank (rank-2) update via Woodbury --------------------------------
U = [([0, 1], [1.0, 0.5]), ([2], [-1.0])]
V = [([1], [0.5]), ([0, 3], [1.0, 0.25])]
lr = LowRankUpdate(lu, U, V, n, k=2)
x2 = lr.solve(b)
Ud = [dense_from_sparse(n, i, vv) for i, vv in U]
Vd = [dense_from_sparse(n, i, vv) for i, vv in V]
A2 = [[A[i][j] + sum(Ud[c][i] * Vd[c][j] for c in range(2))
       for j in range(n)] for i in range(n)]
x_ref2 = LU(A2, n).solve(b)
show("rank-2 update (Woodbury)")
print("x2 =", [round(vv, 6) for vv in x2])
print("max |x2 - x_ref| =",
      f"{max(abs(p - q) for p, q in zip(x2, x_ref2)):.2e}")

# --- singular update: detected, base factorization survives ---------------
show("singular update detection")
col0 = [A[i][0] for i in range(n)]
try:
    RankOneUpdate(lu, list(range(n)), [-c for c in col0], [0], [1.0], n)
except SingularUpdateError as exc:
    print("caught:", exc)
x_still = lu.solve(b)
print("base LU still usable, residual =", f"{residual(A, x_still, b):.2e}")
