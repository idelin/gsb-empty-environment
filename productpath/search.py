"""Best-first search over the vertex x state product space.

Only paths that never repeat a product state ``(vertex, state)`` are
considered, so zero-weight cycles cannot make the search run forever.
Paths are ordered by the three-level key ``(cost, hop count, edge id
sequence)``; the same vertex in different states is never used to
prune another state.  Top-K enumeration is done directly under the
forbidden rules (never by filtering unconstrained results), is
resumable through version-bound cursors, and records the candidate
expansion count plus the budget-pending frontier states.
"""

from __future__ import annotations

import heapq
import itertools
from dataclasses import dataclass, field
from typing import Optional

from .graph import Graph
from .rules import RuleSet


@dataclass(frozen=True)
class Path:
    """A simple path in the product space, with its state trajectory."""

    vertices: tuple[str, ...]
    states: tuple[str, ...]
    edges: tuple[str, ...]
    cost: float

    @property
    def key(self) -> tuple:
        # Three-level ordering: cost, then edge count, then edge ids.
        return (self.cost, len(self.edges), self.edges)

    @property
    def product_states(self) -> tuple[tuple[str, str], ...]:
        return tuple(zip(self.vertices, self.states))


@dataclass(frozen=True)
class Cursor:
    """Resume token for paged top-K enumeration, bound to a rule version."""

    version: int
    last_key: tuple


class StaleCursorError(RuntimeError):
    """Raised when a cursor is used after the rule set changed."""


@dataclass
class SearchResult:
    paths: list[Path]
    cursor: Optional[Cursor]
    expansions: int                 # candidate expansion count
    pending: list[Path] = field(default_factory=list)  # budget-pending frontier
    exhausted: bool = True          # True iff the search space was fully explored

    @property
    def pending_states(self) -> tuple[tuple[str, str], ...]:
        return tuple(
            (p.vertices[-1], p.states[-1]) for p in self.pending
        )


def find_paths(
    graph: Graph,
    rules: RuleSet,
    start: str,
    start_state: str,
    goal: str,
    goal_state: Optional[str] = None,
    k: int = 1,
    budget: Optional[int] = None,
    cursor: Optional[Cursor] = None,
) -> SearchResult:
    """Enumerate the first ``k`` legal paths in key order.

    ``budget`` caps the number of candidate expansions; when it is hit,
    the unexpanded frontier is reported as ``pending``.  ``cursor``
    resumes after a previous page and must match the current rule
    version.
    """
    if k < 1:
        raise ValueError("k must be >= 1")
    if budget is not None and budget < 0:
        raise ValueError("budget must be >= 0")
    if cursor is not None and cursor.version != rules.version:
        raise StaleCursorError(
            f"cursor is bound to rule version {cursor.version}, "
            f"current version is {rules.version}"
        )
    after_key = cursor.last_key if cursor is not None else None

    empty = SearchResult(paths=[], cursor=None, expansions=0)
    if not graph.has_vertex(start) or not graph.has_vertex(goal):
        return empty
    if rules.is_forbidden(start, start_state, 0.0):
        return empty

    counter = itertools.count()
    start_path = Path(vertices=(start,), states=(start_state,), edges=(), cost=0.0)
    heap: list[tuple] = [
        (start_path.key, next(counter), start_path, frozenset({(start, start_state)}))
    ]
    found: list[Path] = []
    expansions = 0
    stopped_by_budget = False
    stopped_by_k = False

    while heap:
        if budget is not None and expansions >= budget:
            stopped_by_budget = True
            break
        _, _, path, seen = heapq.heappop(heap)
        expansions += 1
        last_v = path.vertices[-1]
        last_s = path.states[-1]
        if last_v == goal and (goal_state is None or last_s == goal_state):
            if after_key is None or path.key > after_key:
                found.append(path)
                if len(found) == k:
                    stopped_by_k = True
                    break
            continue  # do not expand past the goal
        for edge in graph.out_edges(last_v):
            if edge.state_in != last_s:
                continue
            nstate = (edge.dst, edge.state_out)
            if nstate in seen:
                continue  # product states may not repeat
            ncost = path.cost + edge.length
            if rules.is_forbidden(edge.dst, edge.state_out, ncost):
                continue
            npath = Path(
                vertices=path.vertices + (edge.dst,),
                states=path.states + (edge.state_out,),
                edges=path.edges + (edge.eid,),
                cost=ncost,
            )
            heapq.heappush(
                heap, (npath.key, next(counter), npath, seen | {nstate})
            )

    pending = [entry[2] for entry in sorted(heap)] if stopped_by_budget else []
    new_cursor = (
        Cursor(version=rules.version, last_key=found[-1].key) if found else None
    )
    return SearchResult(
        paths=found,
        cursor=new_cursor,
        expansions=expansions,
        pending=pending,
        exhausted=not (stopped_by_budget or stopped_by_k),
    )


def shortest_path(
    graph: Graph,
    rules: RuleSet,
    start: str,
    start_state: str,
    goal: str,
    goal_state: Optional[str] = None,
    budget: Optional[int] = None,
) -> SearchResult:
    """Convenience wrapper for the single best legal path."""
    return find_paths(
        graph, rules, start, start_state, goal, goal_state, k=1, budget=budget
    )
