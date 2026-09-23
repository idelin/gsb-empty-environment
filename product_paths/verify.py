"""Independent brute-force checker for the best-first enumeration.

``brute_force_paths`` exhaustively enumerates every legal product-simple
path with an explicit DFS stack (no shared code with the priority-queue
search), sorts them by the three-level key, and is used by
``verify_enumeration`` to confirm that the K-path enumerator is complete and
correctly ordered on small product graphs.
"""

from __future__ import annotations

from dataclasses import dataclass

from .graph import Graph
from .rules import RuleSet
from .search import Path, SearchStats, enumerate_paths


def brute_force_paths(
    graph: Graph,
    ruleset: RuleSet,
    source: str,
    target: str,
    initial_state: str,
    *,
    at: float = 0.0,
    version: int | None = None,
    max_expansions: int | None = None,
) -> tuple[list[Path], SearchStats]:
    """All legal product-simple paths from ``source`` to ``target``, sorted.

    Records the candidate expansion count and, when ``max_expansions`` cuts
    the enumeration short, the end states still pending on the DFS stack.
    """
    version = ruleset.version if version is None else version
    stats = SearchStats()
    found: list[Path] = []

    if ruleset.is_forbidden(source, initial_state, at=at, version=version):
        return found, stats

    stack: list[Path] = [Path((source,), (initial_state,), (), 0.0)]
    stats.generated = 1

    while stack:
        if max_expansions is not None and stats.expansions >= max_expansions:
            stats.budget_exhausted = True
            break
        path = stack.pop()
        stats.expansions += 1
        if path.vertices[-1] == target:
            found.append(path)

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
            stack.append(
                Path(
                    path.vertices + (next_vertex,),
                    path.states + (next_state,),
                    path.edge_ids + (edge.edge_id,),
                    path.cost + edge.length,
                )
            )
            stats.generated += 1

    if stack:
        stats.pending_states = tuple(
            sorted({(p.vertices[-1], p.states[-1]) for p in stack})
        )
    found.sort(key=lambda p: p.sort_key)
    return found, stats


@dataclass
class VerificationReport:
    matches: bool
    compared: int
    expected: list[Path]
    actual: list[Path]
    mismatch_index: int | None
    search_stats: SearchStats
    brute_stats: SearchStats


def verify_enumeration(
    graph: Graph,
    ruleset: RuleSet,
    source: str,
    target: str,
    initial_state: str,
    k: int,
    *,
    at: float = 0.0,
    version: int | None = None,
) -> VerificationReport:
    """Cross-check ``enumerate_paths(k)`` against exhaustive enumeration."""
    result = enumerate_paths(
        graph, ruleset, source, target, initial_state, k, at=at, version=version
    )
    expected, brute_stats = brute_force_paths(
        graph, ruleset, source, target, initial_state, at=at, version=version
    )
    actual = result.paths

    mismatch_index: int | None = None
    for index in range(min(len(actual), len(expected))):
        if actual[index] != expected[index]:
            mismatch_index = index
            break
    if mismatch_index is None and actual != expected[: len(actual)]:
        mismatch_index = min(len(actual), len(expected))
    if mismatch_index is None and len(actual) < k and not result.complete:
        mismatch_index = len(actual)
    if mismatch_index is None and len(expected) <= k and not result.complete:
        mismatch_index = len(expected)

    return VerificationReport(
        matches=mismatch_index is None,
        compared=len(actual),
        expected=expected,
        actual=actual,
        mismatch_index=mismatch_index,
        search_stats=result.stats,
        brute_stats=brute_stats,
    )
