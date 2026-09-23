"""Top-K legal path enumeration in the vertex x state product space.

Paths are ordered by the three-level key (cost, edge count, edge-ID
sequence). Only paths that never repeat a product state (vertex, state) are
considered, which keeps the search space finite even with zero-weight
cycles. Forbidden rules are enforced during expansion (never by filtering
an unconstrained result list), so a shorter path that enters a forbidden
state simply yields to longer legal paths, and different states at the same
vertex never prune each other.
"""

from __future__ import annotations

import heapq
from dataclasses import dataclass

from .graph import Graph
from .rules import RuleSet


class CursorVersionError(ValueError):
    """Raised when a pagination cursor is used against a different ruleset version."""


@dataclass(frozen=True)
class Path:
    """A product-space path: vertices, per-visit states, edge IDs, total cost."""

    vertices: tuple[str, ...]
    states: tuple[str, ...]
    edge_ids: tuple[str, ...]
    cost: float

    @property
    def edge_count(self) -> int:
        return len(self.edge_ids)

    @property
    def sort_key(self) -> tuple:
        """Three-level ordering: cost, then edge count, then edge-ID sequence."""
        return (self.cost, self.edge_count, self.edge_ids)

    def state_trajectory(self) -> tuple[tuple[str, str], ...]:
        """(vertex, state) pairs along the path, including the start."""
        return tuple(zip(self.vertices, self.states))


@dataclass(frozen=True)
class Cursor:
    """Pagination cursor bound to the ruleset version it was issued under."""

    version: int
    last_key: tuple


@dataclass
class SearchStats:
    expansions: int = 0  # candidate paths popped from the frontier and expanded
    generated: int = 0  # candidate paths pushed onto the frontier
    pruned_forbidden: int = 0  # extensions dropped for entering a forbidden state
    pruned_visited: int = 0  # extensions dropped for repeating a product state
    budget_exhausted: bool = False
    pending_states: tuple[tuple[str, str], ...] = ()  # frontier end states left unexpanded


@dataclass
class SearchResult:
    paths: list[Path]
    cursor: Cursor | None
    complete: bool  # True when no further legal paths exist
    stats: SearchStats


def enumerate_paths(
    graph: Graph,
    ruleset: RuleSet,
    source: str,
    target: str,
    initial_state: str,
    k: int,
    *,
    at: float = 0.0,
    version: int | None = None,
    max_expansions: int | None = None,
    cursor: Cursor | None = None,
) -> SearchResult:
    """Enumerate the first ``k`` legal product-simple paths in sort-key order.

    ``at`` is the moment used to evaluate rule intervals; ``version`` pins the
    ruleset snapshot (default: current). ``max_expansions`` caps the number of
    candidate expansions; when the cap is hit the remaining frontier end states
    are reported as ``stats.pending_states``. A ``cursor`` resumes enumeration
    strictly after a previously returned page and must match ``version``.
    """
    if k < 1:
        raise ValueError("k must be >= 1")
    version = ruleset.version if version is None else version
    if cursor is not None and cursor.version != version:
        raise CursorVersionError(
            f"cursor was issued for ruleset version {cursor.version}, "
            f"but this query uses version {version}"
        )

    stats = SearchStats()
    paths: list[Path] = []
    threshold = cursor.last_key if cursor is not None else None

    if ruleset.is_forbidden(source, initial_state, at=at, version=version):
        return SearchResult(paths=[], cursor=cursor, complete=True, stats=stats)

    start = Path((source,), (initial_state,), (), 0.0)
    frontier: list[tuple[tuple, int, Path]] = [(start.sort_key, 0, start)]
    stats.generated = 1
    sequence = 0

    while frontier and len(paths) < k:
        if max_expansions is not None and stats.expansions >= max_expansions:
            stats.budget_exhausted = True
            break
        _, _, path = heapq.heappop(frontier)
        stats.expansions += 1

        already_emitted = threshold is not None and path.sort_key <= threshold
        if path.vertices[-1] == target and not already_emitted:
            paths.append(path)

        end_vertex = path.vertices[-1]
        current_state = path.states[-1]
        visited = set(zip(path.vertices, path.states))
        for edge in graph.outgoing(end_vertex):
            next_state = edge.next_state(current_state)
            if next_state is None:
                continue
            next_vertex = edge.target
            if (next_vertex, next_state) in visited:
                stats.pruned_visited += 1
                continue
            if ruleset.is_forbidden(next_vertex, next_state, at=at, version=version):
                stats.pruned_forbidden += 1
                continue
            extended = Path(
                path.vertices + (next_vertex,),
                path.states + (next_state,),
                path.edge_ids + (edge.edge_id,),
                path.cost + edge.length,
            )
            sequence += 1
            heapq.heappush(frontier, (extended.sort_key, sequence, extended))
            stats.generated += 1

    if frontier:
        stats.pending_states = tuple(
            sorted({(p.vertices[-1], p.states[-1]) for _, _, p in frontier})
        )
    complete = not frontier
    if complete:
        next_cursor = None
    elif paths:
        next_cursor = Cursor(version, paths[-1].sort_key)
    else:
        next_cursor = cursor
    return SearchResult(paths=paths, cursor=next_cursor, complete=complete, stats=stats)
