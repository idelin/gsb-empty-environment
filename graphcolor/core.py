"""Subset-minimal conflict core extraction for proven-infeasible instances.

The core is expressed in terms of the *original* constraints: edge IDs,
precolored vertices and capacity-limited colors. Minimality is by deletion:
removing any single remaining constraint makes the instance feasible. If any
sub-check runs out of budget, `minimal_proven` is False and no minimality
claim is made.
"""

from __future__ import annotations

from dataclasses import dataclass

from .graph import Graph
from .solver import BUDGET_EXHAUSTED, INFEASIBLE, Solver


@dataclass
class ConflictCore:
    edge_ids: list[str]
    precolor_vertices: list[str]
    capacity_colors: list[int]
    minimal_proven: bool

    def describe(self) -> str:
        tag = "subset-minimal" if self.minimal_proven else "not proven minimal (budget)"
        return (
            f"conflict core ({tag}): edges={self.edge_ids} "
            f"precolors={self.precolor_vertices} capacities={self.capacity_colors}"
        )


def find_conflict_core(
    graph: Graph,
    num_colors: int,
    capacities: dict[int, int] | None = None,
    precoloring: dict[str, int] | None = None,
    budget: int = 100_000,
) -> ConflictCore | None:
    """Return a deletion-minimal infeasible sub-instance, or None if the
    instance itself is not proven infeasible within `budget`."""
    capacities = dict(capacities or {})
    precoloring = dict(precoloring or {})
    by_id = {}
    for e in graph.edges:
        if e.id in by_id:
            raise ValueError(f"duplicate edge id {e.id!r}")
        by_id[e.id] = e

    def status(edge_ids, pre, caps) -> str:
        g = Graph()
        for v in graph.vertices:
            g.add_vertex(v)
        for eid in edge_ids:
            e = by_id[eid]
            g.add_edge(e.id, e.u, e.v)
        return Solver(g, num_colors, capacities=caps, precoloring=pre,
                      budget=budget, trace=False).solve().status

    cur_edges = [e.id for e in graph.edges]
    cur_pre = dict(precoloring)
    cur_caps = dict(capacities)
    if status(cur_edges, cur_pre, cur_caps) != INFEASIBLE:
        return None

    minimal = True
    for eid in list(cur_edges):
        trial = [x for x in cur_edges if x != eid]
        st = status(trial, cur_pre, cur_caps)
        if st == INFEASIBLE:
            cur_edges = trial
        elif st == BUDGET_EXHAUSTED:
            minimal = False
    for v in list(cur_pre):
        trial = {k: c for k, c in cur_pre.items() if k != v}
        st = status(cur_edges, trial, cur_caps)
        if st == INFEASIBLE:
            cur_pre = trial
        elif st == BUDGET_EXHAUSTED:
            minimal = False
    for c in list(cur_caps):
        trial = {k: cap for k, cap in cur_caps.items() if k != c}
        st = status(cur_edges, cur_pre, trial)
        if st == INFEASIBLE:
            cur_caps = trial
        elif st == BUDGET_EXHAUSTED:
            minimal = False

    return ConflictCore(sorted(cur_edges), sorted(cur_pre), sorted(cur_caps),
                        minimal)
