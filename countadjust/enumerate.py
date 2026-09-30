"""Brute-force active-set enumeration over exact rationals.

For small models (n <= ~14) this enumerates every candidate active set
S ⊆ {0..n-1}, solves the equality QP with x_S = 0, keeps solutions that
are primally feasible (x >= 0) and dual feasible (z >= 0), and returns
the best objective.  Because all arithmetic is exact, an objective match
with the active-set solver is a *proof* of global optimality for the
bound-constrained convex QP -- not a heuristic cross-check.
"""

from itertools import combinations

from .solver import Status, WLSSolver


def enumerate_active_sets(model, nonneg=True, max_vars=14):
    """Exhaustive rational active-set enumeration.

    Returns ``(best_objective, [x, ...], best_active_sets)`` with all
    optimal vertices, or ``None`` if no feasible KKT point exists.
    """
    n = model.nvars
    if n > max_vars:
        raise ValueError(f"enumeration limited to {max_vars} variables")
    solver = WLSSolver(model, nonneg=nonneg)
    if solver._pre_infeasible_mu is not None:
        return None
    best_obj = None
    best_xs = []
    best_sets = []
    subsets = [frozenset()] if not nonneg else (
        frozenset(c) for r in range(n + 1) for c in combinations(range(n), r)
    )
    for active in subsets:
        out = solver._eqp(active)
        if not isinstance(out, tuple):
            continue  # infeasible or singular subproblem
        x, z, _lam = out
        if nonneg and any(x[i] < 0 for i in range(n) if i not in active):
            continue
        if any(z[i] < 0 for i in active):
            continue
        obj = solver._objective(x)
        if best_obj is None or obj < best_obj:
            best_obj, best_xs, best_sets = obj, [x], [active]
        elif obj == best_obj:
            best_xs.append(x)
            best_sets.append(active)
    if best_obj is None:
        return None
    return best_obj, best_xs, best_sets
