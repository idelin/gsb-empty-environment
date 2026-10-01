"""Search the lattice of per-field generalization-level vectors.

A *node* of the search space is a vector ``(l_1, ..., l_d)`` with
``0 <= l_i <= max_level_i``: one global generalization level per field
(full-domain generalization). Moving "up" increments one coordinate.

Different generalization *paths* (orders in which coordinates are
incremented) converge to the same vector; a visited-set deduplicates them
so every vector is evaluated at most once.

Loss is monotone non-decreasing in every coordinate, so once a vector is
k-anonymous none of its generalizations can have strictly smaller loss and
its subtree is pruned. With unlimited budget the search is therefore exact
(returns a minimum-loss k-anonymous vector). With a finite budget the
search stops early and the status distinguishes "no solution exists" from
"search space not exhausted".
"""

from __future__ import annotations

import enum
from collections import Counter, deque
from dataclasses import dataclass, field

from .hierarchy import GeneralizationHierarchy
from .loss import LossModel


class Status(enum.Enum):
    OPTIMAL = "optimal"                    # exhaustive search, best solution returned
    NO_SOLUTION = "no_solution"            # exhaustive search, nothing satisfies k
    TRUNCATED_BEST = "truncated_best"      # budget hit, best-so-far returned
    TRUNCATED_NONE = "truncated_none"      # budget hit, nothing found so far


@dataclass(frozen=True)
class SearchResult:
    status: Status
    levels: tuple[int, ...] | None
    loss: float | None
    equivalence_class_sizes: tuple[int, ...] | None
    nodes_visited: int
    budget: int | None

    @property
    def solved(self) -> bool:
        return self.levels is not None

    @property
    def exhaustive(self) -> bool:
        return self.status in (Status.OPTIMAL, Status.NO_SOLUTION)


def equivalence_class_sizes(
    records: list[tuple[str, ...]],
    hierarchies: list[GeneralizationHierarchy],
    levels: tuple[int, ...],
) -> tuple[int, ...]:
    """Sorted sizes of the equivalence classes induced by ``levels``."""
    counts: Counter[tuple[str, ...]] = Counter()
    for rec in records:
        counts[tuple(h.generalize(v, l) for h, v, l in zip(hierarchies, rec, levels))] += 1
    return tuple(sorted(counts.values()))


def is_k_anonymous(sizes: tuple[int, ...], k: int) -> bool:
    return bool(sizes) and min(sizes) >= k


@dataclass
class _Frontier:
    queue: deque = field(default_factory=deque)
    visited: set = field(default_factory=set)


def search(
    records: list[tuple[str, ...]],
    hierarchies: list[GeneralizationHierarchy],
    loss_model: LossModel,
    k: int,
    budget: int | None = None,
) -> SearchResult:
    """BFS over the level lattice with monotonicity pruning.

    ``budget`` caps the number of evaluated vectors; ``None`` means
    exhaustive (exact) search. BFS visits vectors in non-decreasing level
    sum, so among ties the least-generalized solution wins.
    """
    if k < 1:
        raise ValueError("k must be >= 1")
    if budget is not None and budget < 1:
        raise ValueError("budget must be >= 1 or None")
    dims = len(hierarchies)
    for rec in records:
        if len(rec) != dims:
            raise ValueError("record arity does not match hierarchies")
        for h, v in zip(hierarchies, rec):
            h.check_value(v)

    origin = (0,) * dims
    frontier = _Frontier(queue=deque([origin]), visited={origin})
    best_levels: tuple[int, ...] | None = None
    best_loss: float | None = None
    best_sizes: tuple[int, ...] | None = None
    visited = 0

    while frontier.queue:
        if budget is not None and visited >= budget:
            break
        levels = frontier.queue.popleft()
        visited += 1
        sizes = equivalence_class_sizes(records, hierarchies, levels)
        if is_k_anonymous(sizes, k):
            loss = loss_model.dataset_loss(records, levels)
            if best_loss is None or loss < best_loss:
                best_levels, best_loss, best_sizes = levels, loss, sizes
            # Prune: generalizations of a k-anonymous vector cannot improve.
            continue
        for i in range(dims):
            if levels[i] < hierarchies[i].max_level:
                child = levels[:i] + (levels[i] + 1,) + levels[i + 1:]
                if child not in frontier.visited:
                    frontier.visited.add(child)
                    frontier.queue.append(child)
    # Exhaustive iff the frontier drained (a budget break leaves it non-empty).
    exhausted = not frontier.queue

    if exhausted:
        status = Status.OPTIMAL if best_levels is not None else Status.NO_SOLUTION
    else:
        status = Status.TRUNCATED_BEST if best_levels is not None else Status.TRUNCATED_NONE
    return SearchResult(
        status=status,
        levels=best_levels,
        loss=best_loss,
        equivalence_class_sizes=best_sizes,
        nodes_visited=visited,
        budget=budget,
    )


def brute_force(
    records: list[tuple[str, ...]],
    hierarchies: list[GeneralizationHierarchy],
    loss_model: LossModel,
    k: int,
) -> SearchResult:
    """Independent exhaustive enumeration of every level vector.

    Used by the test-suite to cross-validate the pruned BFS; also handy for
    tiny instances where clarity matters more than speed.
    """
    import itertools

    dims = len(hierarchies)
    best_levels = best_sizes = None
    best_loss = None
    visited = 0
    ranges = [range(h.max_level + 1) for h in hierarchies]
    for levels in itertools.product(*ranges):
        visited += 1
        sizes = equivalence_class_sizes(records, hierarchies, levels)
        if is_k_anonymous(sizes, k):
            loss = loss_model.dataset_loss(records, levels)
            if best_loss is None or loss < best_loss:
                best_levels, best_loss, best_sizes = levels, loss, sizes
    status = Status.OPTIMAL if best_levels is not None else Status.NO_SOLUTION
    return SearchResult(status, best_levels, best_loss, best_sizes, visited, None)
