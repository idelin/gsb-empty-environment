"""Best-first label-correcting search for the most reliable simple path.

Model
-----
* A *simple path* never repeats a node.
* Every edge belongs to a risk group with success probability p in [0, 1].
* Distinct risk groups fail independently; edges inside one group fail
  together under one common failure event.  Therefore the path success is
  the product over the **distinct** groups the path uses (never a sum of
  probabilities, never the product over repeated shared groups).
* The probability product is kept as a sum of logs (log-space) for
  numerical stability.  p = 0 -> -inf, p = 1 -> 0.0 are handled directly,
  avoiding ``log(0)``.

The best-first queue pops labels in non-increasing log-success order.
Because extending a path can only keep the same success (reusing a group)
or decrease it (entering a new group, p <= 1), the first feasible target
label popped is the proven optimum.  Termination is guaranteed because
only simple paths are generated, hence finitely many labels.
"""

from __future__ import annotations

import heapq
import itertools
import math
from dataclasses import dataclass
from enum import Enum
from fractions import Fraction
from typing import Dict, Hashable, List, Optional

from .model import Graph, NEG_INF, to_fraction


class Status(Enum):
    OPTIMAL = "optimal"        # decided: a feasible optimum was proved
    INFEASIBLE = "infeasible"  # decided: no simple path meets both budgets
    UNDECIDED = "undecided"    # search-effort budget exhausted, no decision yet


@dataclass(frozen=True)
class Label:
    """A simple partial path ending at ``node``.

    ``cost`` / ``delay`` are exact Fractions; ``log_success`` is the log
    of the product over the distinct risk groups in ``groups``.
    """

    node: Hashable
    cost: Fraction
    delay: Fraction
    log_success: float
    groups: frozenset
    visited: frozenset
    path: tuple

    @property
    def success(self) -> float:
        if self.log_success == NEG_INF:
            return 0.0
        return min(1.0, math.exp(self.log_success))


def dominates(a: Label, b: Label) -> bool:
    """Exact dominance of simple-path labels ending at the same node.

    ``a`` dominates ``b`` iff ``a`` uses no more cost and delay, a subset
    of the risk groups (a subset implies log_success(a) >= log_success(b)
    since every group factor is <= 1) and a subset of visited nodes.

    The visited-subset condition is essential for simple paths: it makes
    every simple completion of ``b`` available to ``a`` as well, so
    discarding ``b`` can never delete the optimum or a Pareto point.
    """
    return (
        a.node == b.node
        and a.cost <= b.cost
        and a.delay <= b.delay
        and a.groups <= b.groups
        and a.visited <= b.visited
    )


def frontier_dominates(a: Label, b: Label) -> bool:
    """Pareto dominance on (cost, delay, -log_success) for the frontier."""
    return (
        a.cost <= b.cost
        and a.delay <= b.delay
        and a.log_success >= b.log_success
        and (
            a.cost < b.cost
            or a.delay < b.delay
            or a.log_success > b.log_success
        )
    )


def pareto_frontier(labels: List[Label]) -> List[Label]:
    """Non-dominated (cost, delay, success) triples, cost-sorted."""
    labels = list(labels)
    kept: List[Label] = []
    for i, cand in enumerate(labels):
        if any(j != i and frontier_dominates(other, cand)
               for j, other in enumerate(labels)):
            continue
        kept.append(cand)
    seen = set()
    unique: List[Label] = []
    for label in sorted(kept, key=lambda l: (l.cost, l.delay, -l.log_success)):
        key = (label.cost, label.delay, label.log_success)
        if key not in seen:
            seen.add(key)
            unique.append(label)
    return unique


class SearchState:
    """Mutable, resumable search state (returned inside every result).

    The priority queue, the non-dominated label sets per node and the
    over-budget ``deferred`` labels are all retained.  Resuming with a
    higher ``max_expansions`` continues exactly where the search stopped.
    Resuming with relaxed budgets revives the deferred labels that have
    become feasible, so an INFEASIBLE verdict can be revisited without
    redoing previous work.
    """

    def __init__(self, graph: Graph, source, target,
                 cost_budget: Fraction, delay_budget: Fraction) -> None:
        self.graph = graph
        self.source = source
        self.target = target
        self.cost_budget = cost_budget
        self.delay_budget = delay_budget
        self.heap: List = []
        self.deferred: List[Label] = []
        self.labels: Dict[Hashable, List[Label]] = {}
        self.expansions = 0
        self._tie = itertools.count()
        start = Label(
            source, Fraction(0), Fraction(0), 0.0,
            frozenset(), frozenset({source}), (source,),
        )
        self._push(start)

    def _push(self, label: Label) -> None:
        # max log-success first: negate because heapq is a min-heap
        heapq.heappush(self.heap, (-label.log_success, next(self._tie), label))

    def relax_budgets(self, cost_budget: Fraction, delay_budget: Fraction) -> None:
        self.cost_budget = cost_budget
        self.delay_budget = delay_budget
        still_deferred: List[Label] = []
        for label in self.deferred:
            if label.cost <= cost_budget and label.delay <= delay_budget:
                self._push(label)
            else:
                still_deferred.append(label)
        self.deferred = still_deferred


@dataclass
class SearchResult:
    status: Status
    best: Optional[Label]
    frontier: List[Label]
    state: SearchState

    @property
    def expansions(self) -> int:
        return self.state.expansions


def run_search(graph: Graph, source, target, *,
               cost_budget, delay_budget,
               max_expansions: Optional[int] = None,
               mode: str = "optimal",
               state: Optional[SearchState] = None) -> SearchResult:
    """Search for the most reliable simple path within both budgets.

    Parameters
    ----------
    mode:
        ``"optimal"`` stops at the first feasible target label (proven
        optimum).  ``"frontier"`` exhausts the search and returns the
        complete Pareto frontier of feasible target labels.
    max_expansions:
        Optional cap on expanded labels; if reached before a decision the
        result is :data:`Status.UNDECIDED` (distinct from INFEASIBLE).
    state:
        A previous :class:`SearchState` to resume.
    """
    if mode not in ("optimal", "frontier"):
        raise ValueError("mode must be 'optimal' or 'frontier'")
    cost_budget = to_fraction(cost_budget)
    delay_budget = to_fraction(delay_budget)
    if state is None:
        state = SearchState(graph, source, target, cost_budget, delay_budget)
    else:
        state.relax_budgets(cost_budget, delay_budget)

    while state.heap:
        if max_expansions is not None and state.expansions >= max_expansions:
            return SearchResult(Status.UNDECIDED, None, [], state)
        _, _, label = heapq.heappop(state.heap)
        node_labels = state.labels.setdefault(label.node, [])
        if any(dominates(other, label) for other in node_labels):
            continue
        node_labels[:] = [other for other in node_labels
                          if not dominates(label, other)]
        node_labels.append(label)
        state.expansions += 1

        if label.node == target:
            if mode == "optimal":
                return SearchResult(Status.OPTIMAL, label, [label], state)
            continue  # frontier mode: paths end at the target, do not extend

        _extend(state, label)

    if mode == "frontier":
        frontier = pareto_frontier(state.labels.get(target, []))
        if frontier:
            best = max(frontier, key=lambda l: l.log_success)
            return SearchResult(Status.OPTIMAL, best, frontier, state)
    return SearchResult(Status.INFEASIBLE, None, [], state)


def _extend(state: SearchState, label: Label) -> None:
    for edge in state.graph.out_edges(label.node):
        if edge.head in label.visited:
            continue  # enforce simple paths
        cost = label.cost + edge.cost
        delay = label.delay + edge.delay
        if edge.group in label.groups:
            # shared event already counted: reuse its probability
            log_success = label.log_success
            groups = label.groups
        else:
            log_success = label.log_success + state.graph.group_log(edge.group)
            groups = label.groups | {edge.group}
        nxt = Label(
            edge.head, cost, delay, log_success, groups,
            label.visited | {edge.head}, label.path + (edge.head,),
        )
        if cost <= state.cost_budget and delay <= state.delay_budget:
            state._push(nxt)
        else:
            state.deferred.append(nxt)  # revivable after budget relaxation
