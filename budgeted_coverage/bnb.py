"""Resumable branch-and-bound exact solver.

Design notes
------------
* Binary include/exclude branching over sets ordered by value density
  (zero-cost sets first).  Including a set pulls in its dependency closure.
* Every node keeps a *selected* prefix that is always feasible, so the
  incumbent is updated at every node, not only at leaves.
* The upper bound at a node is ``current profit + optimistic remainder``
  where the remainder overestimates per-set marginals (each uncovered
  element may be counted by several sets) and packs them into the remaining
  budget as a fractional knapsack; zero-cost sets contribute their full
  optimistic marginal without consuming budget.  The bound is additionally
  capped by the total weight of not-yet-satisfied elements.  Both terms are
  valid overestimates, so their minimum is a reliable upper bound.
* Every pruned node is logged with the numeric reason (bound vs incumbent,
  cost vs budget, conflicting pair, unsatisfiable dependency, ...).
* The whole search state (explicit DFS stack, incumbent, counters, prune
  log) serialises to JSON and can be resumed later.  A checkpoint is bound
  to the instance fingerprint: if the instance changed (e.g. a set was
  deleted), resuming raises ``CheckpointMismatch`` and the old incumbent
  must be re-validated.
"""
from __future__ import annotations

import json
from dataclasses import dataclass, field
from fractions import Fraction
from typing import FrozenSet, List, Optional

from .model import Instance, Status, UnsatisfiableDependency


class CheckpointMismatch(Exception):
    """Checkpoint belongs to a different (e.g. modified) instance."""


@dataclass
class PruneRecord:
    node_id: int
    depth: int
    reason: str
    detail: str


@dataclass
class _Node:
    index: int
    selected: FrozenSet[str]
    excluded: FrozenSet[str]
    bound: Optional[Fraction]  # valid upper bound for the subtree, None = not computed


@dataclass
class BnBResult:
    status: Status
    selected: FrozenSet[str]
    profit: Fraction
    upper_bound: Fraction
    nodes_explored: int
    prunes: List[PruneRecord] = field(default_factory=list)
    fingerprint: str = ""


def _density_key(instance: Instance, name: str):
    s = instance.sets[name]
    weight = sum(instance.weights.get(e, Fraction(0)) for e in s.elements)
    if s.cost == 0:
        return (0, Fraction(0), name)          # zero-cost sets branch first
    return (1, -weight / s.cost, name)


class BranchAndBound:
    def __init__(self, instance: Instance):
        self.instance = instance
        self.order = sorted(instance.sets, key=lambda n: _density_key(instance, n))
        root = _Node(0, frozenset(), frozenset(), None)
        root.bound = self._bound(root.selected, root.excluded, root.index)
        self.stack: List[_Node] = [root]
        self.best_selected: FrozenSet[str] = frozenset()
        self.best_profit: Fraction = instance.profit_of(())
        self.nodes_explored = 0
        self.prunes: List[PruneRecord] = []

    # ------------------------------------------------------------- bounding

    def _bound(self, selected, excluded, index) -> Fraction:
        inst = self.instance
        counts = inst.coverage_counts(selected)
        base = inst.profit_from_counts(counts)
        remaining_budget = inst.budget - inst.total_cost(selected)
        total = base
        packable = []  # (optimistic marginal, cost) for costly candidates
        for name in self.order[index:]:
            if name in selected or name in excluded:
                continue
            s = inst.sets[name]
            marg = inst.optimistic_marginal(counts, s.elements)
            if marg <= 0:
                continue
            if s.cost == 0:
                total += marg  # free: add fully, no budget arithmetic
            else:
                packable.append((marg, s.cost))
        packable.sort(key=lambda mc: (mc[0] / mc[1], mc[0]), reverse=True)
        budget_left = remaining_budget
        for marg, cost in packable:
            if budget_left <= 0:
                break
            take = cost if cost <= budget_left else budget_left
            total += marg * take / cost
            budget_left -= take
        residual = sum(
            (w for e, w in inst.weights.items() if counts.get(e, 0) < inst.required_cover(e)),
            Fraction(0),
        )
        return min(total, base + residual)

    # ------------------------------------------------------------- search

    def _prune(self, node: _Node, reason: str, detail: str):
        self.prunes.append(
            PruneRecord(self.nodes_explored, node.index, reason, detail)
        )

    def _push_child(self, node: _Node, selected, excluded):
        bound = self._bound(selected, excluded, node.index + 1)
        if bound <= self.best_profit:
            self._prune(
                node,
                "bound",
                f"subtree upper bound {bound} <= incumbent {self.best_profit}",
            )
            return
        self.stack.append(_Node(node.index + 1, selected, excluded, bound))

    def _push_include(self, node: _Node, name: str):
        inst = self.instance
        try:
            closure = inst.dependency_closure(set(node.selected) | {name})
        except UnsatisfiableDependency as exc:
            self._prune(
                node,
                "dependency-unsatisfiable",
                f"including {name!r} requires missing set {exc.missing!r}",
            )
            return
        added = frozenset(closure - node.selected)
        blocked = sorted(added & node.excluded)
        if blocked:
            self._prune(
                node,
                "dependency-excluded",
                f"including {name!r} forces previously excluded sets {blocked}",
            )
            return
        pairs = inst.conflicting_pairs(closure)
        if pairs:
            self._prune(
                node,
                "conflict",
                f"including {name!r} creates mutually exclusive pairs {pairs}",
            )
            return
        cost = inst.total_cost(closure)
        if cost > inst.budget:
            self._prune(
                node,
                "budget",
                f"including {name!r} costs {cost} > budget {inst.budget}",
            )
            return
        self._push_child(node, frozenset(closure), node.excluded)

    def solve(self, max_nodes: Optional[int] = None) -> BnBResult:
        while self.stack:
            if max_nodes is not None and self.nodes_explored >= max_nodes:
                return self._result(Status.BUDGET_UNDECIDED)
            node = self.stack.pop()
            self.nodes_explored += 1
            profit = self.instance.profit_of(node.selected)
            if profit > self.best_profit:
                self.best_profit = profit
                self.best_selected = node.selected
            if node.index >= len(self.order):
                continue
            name = self.order[node.index]
            if name in node.selected:
                # Already forced in by a dependency closure; just advance.
                self.stack.append(
                    _Node(node.index + 1, node.selected, node.excluded, node.bound)
                )
                continue
            # Exclude branch first so the include branch is explored first.
            self._push_child(node, node.selected, node.excluded | {name})
            self._push_include(node, name)
        return self._result(Status.OPTIMAL)

    def _result(self, status: Status) -> BnBResult:
        if status is Status.OPTIMAL:
            upper = self.best_profit  # search exhausted: bound is tight
        else:
            live = [n.bound for n in self.stack if n.bound is not None]
            upper = max([self.best_profit, *live])
        return BnBResult(
            status,
            self.best_selected,
            self.best_profit,
            upper,
            self.nodes_explored,
            list(self.prunes),
            self.instance.fingerprint(),
        )

    # ------------------------------------------------------------- resume

    def save_checkpoint(self, path: str):
        data = {
            "fingerprint": self.instance.fingerprint(),
            "stack": [
                {
                    "index": n.index,
                    "selected": sorted(n.selected),
                    "excluded": sorted(n.excluded),
                    "bound": None if n.bound is None else str(n.bound),
                }
                for n in self.stack
            ],
            "best_selected": sorted(self.best_selected),
            "best_profit": str(self.best_profit),
            "nodes_explored": self.nodes_explored,
            "prunes": [vars(p) for p in self.prunes],
        }
        with open(path, "w", encoding="utf-8") as fh:
            json.dump(data, fh, indent=2)

    @classmethod
    def load(cls, instance: Instance, path: str) -> "BranchAndBound":
        with open(path, encoding="utf-8") as fh:
            data = json.load(fh)
        if data["fingerprint"] != instance.fingerprint():
            raise CheckpointMismatch(
                "checkpoint fingerprint "
                + str(data["fingerprint"])
                + " != instance "
                + instance.fingerprint()
                + "; the instance changed, re-solve from scratch"
            )
        solver = cls(instance)
        solver.stack = [
            _Node(
                n["index"],
                frozenset(n["selected"]),
                frozenset(n["excluded"]),
                None if n["bound"] is None else Fraction(n["bound"]),
            )
            for n in data["stack"]
        ]
        solver.best_selected = frozenset(data["best_selected"])
        solver.best_profit = Fraction(data["best_profit"])
        solver.nodes_explored = data["nodes_explored"]
        solver.prunes = [PruneRecord(**p) for p in data["prunes"]]
        return solver
