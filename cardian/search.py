"""Branch-and-bound over count vectors, with resumable state.

The searcher explores assignments of the per-tuple-type count variables.
It tracks, at all times:

  * best_min / best_max  - objective values of the best *feasible* instances
                           found so far (each with a concrete witness),
  * proven bounds        - bounds that also cover every not-yet-explored
                           branch, so they remain valid when the run is
                           interrupted by the node budget.

State (frontier + incumbents + counters) serializes to JSON and can be
resumed later; a problem fingerprint prevents resuming against modified
statistics or a modified query.
"""
from __future__ import annotations

from .model import canonical_key
from .propagate import tighten


# ---------------------------------------------------------------------------
# Objectives
# ---------------------------------------------------------------------------

class SelectObjective:
    """Linear objective: number of rows of one table satisfying WHERE."""

    def __init__(self, coeff):
        self.coeff = tuple(coeff)

    def relax_min(self, lb, ub):
        return sum(c * l for c, l in zip(self.coeff, lb))

    def relax_max(self, lb, ub):
        return sum(c * u for c, u in zip(self.coeff, ub))

    def evaluate(self, assign):
        return sum(c * a for c, a in zip(self.coeff, assign))

    def spec(self):
        return {"select": list(self.coeff)}


class JoinObjective:
    """sum_k A_k * B_k over join-key values, where A_k / B_k are the counts
    of filter-passing rows with that (non-null) key in the left/right table."""

    def __init__(self, key_vars):
        # key_vars: list of (key, left_var_indices, right_var_indices)
        self.key_vars = [
            (k, tuple(lv), tuple(rv)) for k, lv, rv in sorted(key_vars, key=lambda t: t[0])
        ]

    @staticmethod
    def _sums(vec, key_vars):
        for _k, lv, rv in key_vars:
            a = sum(vec[i] for i in lv)
            b = sum(vec[i] for i in rv)
            yield a, b

    def relax_min(self, lb, ub):
        return sum(a * b for a, b in self._sums(lb, self.key_vars))

    def relax_max(self, lb, ub):
        return sum(a * b for a, b in self._sums(ub, self.key_vars))

    def evaluate(self, assign):
        return sum(a * b for a, b in self._sums(assign, self.key_vars))

    def spec(self):
        return {"join": [[k, list(lv), list(rv)] for k, lv, rv in self.key_vars]}


def problem_fingerprint(constraints, objective):
    payload = {
        "constraints": [[c.name, list(c.vars), c.target] for c in constraints],
        "objective": objective.spec(),
    }
    return canonical_key(payload)


# ---------------------------------------------------------------------------
# Searcher
# ---------------------------------------------------------------------------

class Searcher:
    def __init__(self, constraints, n_vars, objective, fingerprint=None):
        self.constraints = list(constraints)
        self.n_vars = n_vars
        self.objective = objective
        self.fingerprint = (
            fingerprint
            if fingerprint is not None
            else problem_fingerprint(self.constraints, self.objective)
        )
        big = max((c.target for c in self.constraints), default=0)
        root = tighten([0] * n_vars, [big] * n_vars, self.constraints)
        self.frontier = [tuple(root)] if root is not None else []
        self.best_min = None        # smallest feasible objective seen
        self.best_max = None        # largest feasible objective seen
        self.witness_min = None     # assignment achieving best_min
        self.witness_max = None     # assignment achieving best_max
        self.nodes = 0              # nodes actually explored
        self.leaves = 0             # feasible leaves evaluated
        self.pruned_infeasible = 0  # nodes killed by propagation
        self.pruned_bound = 0       # nodes that cannot improve either bound

    # -- main loop ---------------------------------------------------------

    def run(self, budget):
        """Explore at most `budget` further nodes.  Returns True iff the
        search is complete (frontier exhausted)."""
        while self.frontier and self.nodes < budget:
            lb, ub = self.frontier.pop()
            self.nodes += 1
            tightened = tighten(lb, ub, self.constraints)
            if tightened is None:
                self.pruned_infeasible += 1
                continue
            lb, ub = tightened
            rmin = self.objective.relax_min(lb, ub)
            rmax = self.objective.relax_max(lb, ub)
            if (
                self.best_min is not None
                and rmin >= self.best_min
                and rmax <= self.best_max
            ):
                # No feasible completion of this branch can extend the
                # observed objective range: prune by bound.
                self.pruned_bound += 1
                continue
            if lb == ub:
                self.leaves += 1
                val = self.objective.evaluate(lb)
                if self.best_min is None or val < self.best_min:
                    self.best_min = val
                    self.witness_min = list(lb)
                if self.best_max is None or val > self.best_max:
                    self.best_max = val
                    self.witness_max = list(lb)
                continue
            i = max(range(self.n_vars), key=lambda j: (ub[j] - lb[j], -j))
            mid = (lb[i] + ub[i]) // 2
            lo_child = (list(lb), list(ub))
            lo_child[1][i] = mid
            hi_child = (list(lb), list(ub))
            hi_child[0][i] = mid + 1
            self.frontier.append(lo_child)
            self.frontier.append(hi_child)
        return not self.frontier

    # -- certified bounds ---------------------------------------------------

    def proven_bounds(self):
        """Returns (lower, upper, exact).

        lower/upper cover *all* branches, explored or not:
          lower <= true minimum, upper >= true maximum.
        When exact is True, lower == best_min and upper == best_max.
        """
        if not self.frontier:
            return self.best_min, self.best_max, True
        lo = self.best_min
        hi = self.best_max
        for lb, ub in self.frontier:
            rmin = self.objective.relax_min(lb, ub)
            rmax = self.objective.relax_max(lb, ub)
            lo = rmin if lo is None else min(lo, rmin)
            hi = rmax if hi is None else max(hi, rmax)
        return lo, hi, False

    # -- (de)serialization ---------------------------------------------------

    def to_json(self):
        return {
            "fingerprint": self.fingerprint,
            "frontier": [[list(lb), list(ub)] for lb, ub in self.frontier],
            "best_min": self.best_min,
            "best_max": self.best_max,
            "witness_min": self.witness_min,
            "witness_max": self.witness_max,
            "nodes": self.nodes,
            "leaves": self.leaves,
            "pruned_infeasible": self.pruned_infeasible,
            "pruned_bound": self.pruned_bound,
        }

    @classmethod
    def from_json(cls, data, constraints, n_vars, objective):
        fp = problem_fingerprint(constraints, objective)
        if data.get("fingerprint") != fp:
            raise ValueError(
                "saved search state does not match the current statistics/query "
                "(fingerprint mismatch); stale proofs must not be reused"
            )
        s = cls.__new__(cls)
        s.constraints = list(constraints)
        s.n_vars = n_vars
        s.objective = objective
        s.fingerprint = fp
        s.frontier = [(tuple(lb), tuple(ub)) for lb, ub in data["frontier"]]
        s.best_min = data["best_min"]
        s.best_max = data["best_max"]
        s.witness_min = data["witness_min"]
        s.witness_max = data["witness_max"]
        s.nodes = data["nodes"]
        s.leaves = data["leaves"]
        s.pruned_infeasible = data["pruned_infeasible"]
        s.pruned_bound = data["pruned_bound"]
        return s
