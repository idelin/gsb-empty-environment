"""Integer count constraints and bound-tightening propagation.

Every statistic becomes one linear equality  sum(x_i for i in vars) == target
over the per-tuple-type count variables.  Propagation tightens [lb, ub] of
each variable against every constraint that contains it, to a fixpoint.
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Constraint:
    name: str                # human-readable, used in conflict (IIS) reports
    vars: tuple              # variable indices summed by this constraint
    target: int              # exact required sum


def tighten(lb, ub, constraints):
    """Propagate all constraints.  Returns (lb, ub) lists, or None if the
    bounds are provably infeasible."""
    lb = list(lb)
    ub = list(ub)
    changed = True
    while changed:
        changed = False
        for c in constraints:
            sum_lb = 0
            sum_ub = 0
            for i in c.vars:
                sum_lb += lb[i]
                sum_ub += ub[i]
            if c.target < sum_lb or c.target > sum_ub:
                return None
            room = c.target - sum_lb      # total slack above lower bounds
            excess = sum_ub - c.target    # total excess above the target
            for i in c.vars:
                nub = ub[i] if ub[i] < room + lb[i] else room + lb[i]
                nlb = lb[i] if lb[i] > ub[i] - excess else ub[i] - excess
                if nlb > nub:
                    return None
                if nub != ub[i]:
                    ub[i] = nub
                    changed = True
                if nlb != lb[i]:
                    lb[i] = nlb
                    changed = True
    return lb, ub


def _initial_bounds(n_vars, constraints):
    big = max((c.target for c in constraints), default=0)
    return [0] * n_vars, [big] * n_vars


def _branch(lb, ub):
    """Pick the variable with the widest remaining range (lowest index wins)."""
    i = max(range(len(lb)), key=lambda j: (ub[j] - lb[j], -j))
    mid = (lb[i] + ub[i]) // 2
    lo_child = (list(lb), list(ub))
    lo_child[1][i] = mid
    hi_child = (list(lb), list(ub))
    hi_child[0][i] = mid + 1
    return lo_child, hi_child


def find_feasible(constraints, n_vars, budget=1_000_000):
    """Complete feasibility search.  Returns a concrete assignment (list),
    None if provably infeasible, or the string 'unknown' on budget exhaustion."""
    lb, ub = _initial_bounds(n_vars, constraints)
    root = tighten(lb, ub, constraints)
    if root is None:
        return None
    stack = [tuple(root)]
    nodes = 0
    while stack:
        if nodes >= budget:
            return "unknown"
        lb, ub = stack.pop()
        nodes += 1
        tightened = tighten(lb, ub, constraints)
        if tightened is None:
            continue
        lb, ub = tightened
        if lb == ub:
            return list(lb)
        lo_child, hi_child = _branch(lb, ub)
        stack.append(lo_child)
        stack.append(hi_child)
    return None


def find_iis(constraints, n_vars, budget=1_000_000):
    """Deletion-minimal irreducible inconsistent subset.

    The returned list of constraint names is inconsistent as a whole, and
    removing any single member makes it consistent.
    """
    iis = list(constraints)
    for c in list(iis):
        trial = [x for x in iis if x is not c]
        verdict = find_feasible(trial, n_vars, budget)
        if verdict == "unknown":
            raise RuntimeError("IIS check exceeded feasibility budget")
        if verdict is None:
            iis = trial
    return [c.name for c in iis]
