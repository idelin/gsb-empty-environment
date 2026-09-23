"""Branch-and-bound search over joint distributions.

A "joint distribution" for the relations in a query is a non-negative
integer multiplicity for every possible tuple (cell) of every involved
relation. Statistics are linear integer constraints over cell
multiplicities. The search enumerates all assignments satisfying the
constraints and tracks the exact minimum and maximum of the query row
count, each with a concrete witness.

The search is resumable: the explicit DFS stack can be serialized to JSON
and restored later. Restoring requires the same specification fingerprint;
if any statistic changed, the saved state (and its bounds) is stale and
must be discarded.
"""
from __future__ import annotations

from .query import eval_predicate


class StaleStateError(ValueError):
    """Raised when resuming a search state whose spec fingerprint differs."""


def propagate(assign, constraints, cell_cons, ncells):
    """Constraint propagation over partial assignments.

    assign: dict cell_index -> multiplicity (not mutated).
    constraints: list of (cells, target).
    cell_cons: list per cell of constraint indices containing it.

    Returns (assign, ubs) with forced cells assigned and an upper bound for
    every still-unassigned cell, or None if the partial assignment is
    infeasible.
    """
    assign = dict(assign)
    changed = True
    while changed:
        changed = False
        for cells, target in constraints:
            known = 0
            unknown = []
            for c in cells:
                if c in assign:
                    known += assign[c]
                else:
                    unknown.append(c)
            rem = target - known
            if rem < 0:
                return None
            if not unknown:
                if rem != 0:
                    return None
            elif rem == 0:
                for c in unknown:
                    assign[c] = 0
                changed = True
            elif len(unknown) == 1:
                assign[unknown[0]] = rem
                changed = True
    ubs = {}
    for c in range(ncells):
        if c in assign:
            continue
        cons = cell_cons[c]
        if not cons:
            # A cell untouched by any constraint can be fixed to 0 without
            # loss: no statistic references it, so some feasible optimum
            # exists with multiplicity 0 here.
            assign[c] = 0
            continue
        ub = None
        for ci in cons:
            cells, target = constraints[ci]
            known = sum(assign[k] for k in cells if k in assign)
            rem = target - known
            if rem < 0:
                return None
            ub = rem if ub is None else min(ub, rem)
        ubs[c] = ub
    return assign, ubs


class SelectObjective:
    """Row count of a single-relation filtered query (linear objective)."""

    def __init__(self, cells, columns, where):
        self.pred_true = []
        for _rel, tup in cells:
            row = dict(zip(columns, tup))
            if where is None:
                self.pred_true.append(True)
            else:
                self.pred_true.append(
                    eval_predicate(where, lambda c, r=row: r[c]) is True
                )

    def bounds(self, assign, ubs):
        lo = 0
        for c, v in assign.items():
            if self.pred_true[c]:
                lo += v
        hi = lo
        for c, ub in ubs.items():
            if self.pred_true[c]:
                hi += ub
        return lo, hi

    def value(self, assign):
        return sum(v for c, v in assign.items() if self.pred_true[c])


class JoinObjective:
    """Row count of a two-table equi-inner-join with a filter."""

    def __init__(self, cells, rel_names, columns_by_rel, on_pairs, where):
        # cells: list of (rel, tuple); rel_names: [left, right]
        self.left, self.right = rel_names
        left_cols, right_cols = columns_by_rel
        self.keys = {}
        left_idx = {c: i for i, c in enumerate(left_cols)}
        right_idx = {c: i for i, c in enumerate(right_cols)}
        left_cells = {}
        right_cells = {}
        for i, (rel, tup) in enumerate(cells):
            if rel == self.left:
                key = tuple(tup[left_idx[c]] for c, _ in on_pairs)
                if any(v is None for v in key):
                    continue  # NULL never matches in an equi-join
                left_cells.setdefault(key, []).append(i)
            else:
                key = tuple(tup[right_idx[c]] for _, c in on_pairs)
                if any(v is None for v in key):
                    continue
                right_cells.setdefault(key, []).append(i)
        self.left_cells = left_cells
        self.right_cells = right_cells
        # Precompute which (left_cell, right_cell) pairs satisfy the filter.
        self.true_pairs = {}
        row_cache = {}
        for key in left_cells:
            if key not in right_cells:
                continue
            pairs = []
            for li in left_cells[key]:
                lrow = row_cache.get(li)
                if lrow is None:
                    lrow = dict(zip(left_cols, cells[li][1]))
                    row_cache[li] = lrow
                for ri in right_cells[key]:
                    rrow = row_cache.get(ri)
                    if rrow is None:
                        rrow = dict(zip(right_cols, cells[ri][1]))
                        row_cache[ri] = rrow
                    if where is None or eval_predicate(
                        where, _join_getter(self.left, lrow, self.right, rrow)
                    ) is True:
                        pairs.append((li, ri))
            self.true_pairs[key] = pairs

    def bounds(self, assign, ubs):
        lo = 0
        for pairs in self.true_pairs.values():
            for li, ri in pairs:
                if li in assign and ri in assign:
                    lo += assign[li] * assign[ri]
        hi = 0
        for key, lcells in self.left_cells.items():
            rcells = self.right_cells.get(key)
            if not rcells:
                continue
            lhi = sum(assign.get(c, ubs.get(c, 0)) for c in lcells)
            rhi = sum(assign.get(c, ubs.get(c, 0)) for c in rcells)
            hi += lhi * rhi
        return lo, hi

    def value(self, assign):
        total = 0
        for pairs in self.true_pairs.values():
            for li, ri in pairs:
                total += assign.get(li, 0) * assign.get(ri, 0)
        return total


def _join_getter(lname, lrow, rname, rrow):
    def get(ref):
        rel, col = ref.split(".", 1)
        return lrow[col] if rel == lname else rrow[col]

    return get


class _Frame:
    __slots__ = ("cell", "candidates", "pos", "assign", "lo", "hi")

    def __init__(self, cell, candidates, assign, lo, hi):
        self.cell = cell
        self.candidates = candidates
        self.pos = 0
        self.assign = assign
        self.lo = lo
        self.hi = hi


class Searcher:
    """Depth-first branch-and-bound over cell multiplicities."""

    def __init__(self, ncells, constraints, objective=None):
        self.ncells = ncells
        self.constraints = constraints
        self.cell_cons = [[] for _ in range(ncells)]
        for ci, (cells, _target) in enumerate(constraints):
            for c in cells:
                self.cell_cons[c].append(ci)
        self.objective = objective
        self.nodes = 0
        self.prunes = 0
        self.best_min = None  # (value, assign)
        self.best_max = None
        self.stack = []
        self.witness = None  # feasibility mode: first feasible assignment

    # -- internal helpers -------------------------------------------------

    def _propagate(self, assign):
        return propagate(assign, self.constraints, self.cell_cons, self.ncells)

    def _choose_cell(self, ubs):
        # fail-first: branch on the unassigned cell with the smallest bound
        return min(ubs, key=lambda c: (ubs[c], c))

    def _push_frame(self, assign, ubs):
        if self.objective is not None:
            lo, hi = self.objective.bounds(assign, ubs)
        else:
            lo, hi = 0, 0
        cell = self._choose_cell(ubs)
        candidates = list(range(ubs[cell] + 1))
        self.stack.append(_Frame(cell, candidates, assign, lo, hi))

    # -- main loop ---------------------------------------------------------

    def run(self, budget, state=None):
        """Run the search. Returns a result dict (see module docstring)."""
        if state is not None:
            self._restore(state)
        else:
            root = self._propagate({})
            if root is None:
                return self._result("infeasible")
            assign, ubs = root
            if not ubs:
                self._visit_leaf(assign)
                if self.objective is None:
                    return self._result("feasible")
            else:
                self._push_frame(assign, ubs)
        while self.stack:
            if self.nodes >= budget:
                return self._result("budget_exhausted")
            frame = self.stack[-1]
            if frame.pos == len(frame.candidates):
                self.stack.pop()
                continue
            value = frame.candidates[frame.pos]
            frame.pos += 1
            self.nodes += 1
            trial = dict(frame.assign)
            trial[frame.cell] = value
            prop = self._propagate(trial)
            if prop is None:
                self.prunes += 1
                continue
            assign, ubs = prop
            if self.objective is not None:
                lo, hi = self.objective.bounds(assign, ubs)
                # A subtree is useless only when it can improve neither the
                # minimum (its lower bound is not below the best minimum)
                # nor the maximum (its upper bound is not above the best max).
                hopeless_min = self.best_min is not None and lo >= self.best_min[0]
                hopeless_max = self.best_max is not None and hi <= self.best_max[0]
                if hopeless_min and hopeless_max:
                    self.prunes += 1
                    continue
            if not ubs:
                self._visit_leaf(assign)
                if self.objective is None:
                    return self._result("feasible")
                continue
            self._push_frame(assign, ubs)
        if self.objective is None:
            return self._result("infeasible")
        return self._result("exact")

    def _visit_leaf(self, assign):
        if self.objective is None:
            self.witness = assign
            return
        value = self.objective.value(assign)
        if self.best_min is None or value < self.best_min[0]:
            self.best_min = (value, dict(assign))
        if self.best_max is None or value > self.best_max[0]:
            self.best_max = (value, dict(assign))

    def _result(self, status):
        out = {
            "status": status,
            "nodes_explored": self.nodes,
            "nodes_pruned": self.prunes,
        }
        if self.objective is None:
            out["witness"] = self.witness
            return out
        if status == "budget_exhausted":
            out["state"] = self.serialize()
            if self.stack:
                out["proven_lower"] = min(f.lo for f in self.stack)
                out["proven_upper"] = max(f.hi for f in self.stack)
            else:  # pragma: no cover - defensive
                out["proven_lower"] = self.best_min[0] if self.best_min else None
                out["proven_upper"] = self.best_max[0] if self.best_max else None
        else:
            out["proven_lower"] = self.best_min[0] if self.best_min else None
            out["proven_upper"] = self.best_max[0] if self.best_max else None
        out["best_min"] = self.best_min
        out["best_max"] = self.best_max
        return out

    # -- serialization -----------------------------------------------------

    def serialize(self):
        def pack_best(best):
            if best is None:
                return None
            return {"value": best[0], "assign": {str(k): v for k, v in best[1].items()}}

        return {
            "version": 1,
            "nodes_explored": self.nodes,
            "nodes_pruned": self.prunes,
            "best_min": pack_best(self.best_min),
            "best_max": pack_best(self.best_max),
            "stack": [
                {
                    "cell": f.cell,
                    "candidates": f.candidates,
                    "pos": f.pos,
                    "assign": {str(k): v for k, v in f.assign.items()},
                    "lo": f.lo,
                    "hi": f.hi,
                }
                for f in self.stack
            ],
        }

    def _restore(self, state):
        if state.get("version") != 1:
            raise ValueError("unsupported search state version")
        self.nodes = state["nodes_explored"]
        self.prunes = state["nodes_pruned"]

        def unpack_best(packed):
            if packed is None:
                return None
            return (
                packed["value"],
                {int(k): v for k, v in packed["assign"].items()},
            )

        self.best_min = unpack_best(state["best_min"])
        self.best_max = unpack_best(state["best_max"])
        self.stack = []
        for fs in state["stack"]:
            frame = _Frame(
                fs["cell"],
                list(fs["candidates"]),
                {int(k): v for k, v in fs["assign"].items()},
                fs["lo"],
                fs["hi"],
            )
            frame.pos = fs["pos"]
            self.stack.append(frame)
