"""Independent exhaustive enumeration for small product graphs.

Used to check the completeness of :func:`productpath.search.find_paths`:
it depth-first enumerates every legal simple path in the product space
and sorts them by the same three-level key.
"""

from __future__ import annotations

from typing import Optional

from .graph import Graph
from .rules import RuleSet
from .search import Path


def brute_force_paths(
    graph: Graph,
    rules: RuleSet,
    start: str,
    start_state: str,
    goal: str,
    goal_state: Optional[str] = None,
) -> list[Path]:
    results: list[Path] = []
    if not graph.has_vertex(start) or not graph.has_vertex(goal):
        return results
    if rules.is_forbidden(start, start_state, 0.0):
        return results

    def dfs(path: Path, seen: frozenset) -> None:
        last_v = path.vertices[-1]
        last_s = path.states[-1]
        if last_v == goal and (goal_state is None or last_s == goal_state):
            results.append(path)
            return
        for edge in graph.out_edges(last_v):
            if edge.state_in != last_s:
                continue
            nstate = (edge.dst, edge.state_out)
            if nstate in seen:
                continue
            ncost = path.cost + edge.length
            if rules.is_forbidden(edge.dst, edge.state_out, ncost):
                continue
            dfs(
                Path(
                    vertices=path.vertices + (edge.dst,),
                    states=path.states + (edge.state_out,),
                    edges=path.edges + (edge.eid,),
                    cost=ncost,
                ),
                seen | {nstate},
            )

    dfs(
        Path(vertices=(start,), states=(start_state,), edges=(), cost=0.0),
        frozenset({(start, start_state)}),
    )
    results.sort(key=lambda p: p.key)
    return results
