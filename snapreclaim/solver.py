"""Exact branch-and-bound solvers.

Two objective modes are provided:

* ``solve_min_loss``: minimise total business loss subject to reclaiming
  at least ``required_bytes``.  Ties are broken by fewer deleted
  snapshots, then by lexicographically smallest sorted id list.
* ``solve_max_reclaim``: maximise reclaimed bytes subject to a loss
  budget.  Ties are broken by lower loss, fewer deletions, then
  lexicographically smallest sorted id list.

Both solvers are exact (branch and bound): when they finish without
hitting a node/time limit, the returned solution is proven optimal.
The search explicitly tracks shared-block reference counts, so the
complementarity of shared blocks (a block is freed only when its last
referring snapshot is deleted) is fully accounted for.
"""
from __future__ import annotations

import sys
import time
from dataclasses import dataclass, field
from typing import List, Optional

from .model import Problem


class _AbortSearch(Exception):
    """Internal: unwind the search when a node/time limit is hit."""


@dataclass
class SearchResult:
    feasible: bool
    selected: List[str] = field(default_factory=list)
    total_loss: int = 0
    reclaimed_bytes: int = 0
    proven_optimal: bool = False
    nodes_explored: int = 0
    elapsed_seconds: float = 0.0

    def to_dict(self) -> dict:
        return {
            "feasible": self.feasible,
            "selected": list(self.selected),
            "total_loss": self.total_loss,
            "reclaimed_bytes": self.reclaimed_bytes,
            "proven_optimal": self.proven_optimal,
            "nodes_explored": self.nodes_explored,
            "elapsed_seconds": round(self.elapsed_seconds, 6),
        }


class _Search:
    """Shared branch-and-bound machinery.

    Snapshots are decided in a fixed order.  Per block we track:
      live[b]      - references from undecided or kept (excluded) snapshots;
                     a block is reclaimed exactly when live[b] reaches 0.
      undecided[b] - references from snapshots not yet decided.
    ``potential`` sums the sizes of blocks that could still be freed,
    i.e. live[b] == undecided[b] > 0 (no kept snapshot pins the block).
    It is a valid optimistic bound on additional reclaimable bytes.
    """

    def __init__(self, problem: Problem, node_limit: Optional[int],
                 time_limit: Optional[float]):
        self.problem = problem
        self.node_limit = node_limit
        self.time_limit = time_limit
        self.start = time.monotonic()
        self.nodes = 0

        self.block_ids = sorted(problem.block_sizes)
        self.sizes = [problem.block_sizes[b] for b in self.block_ids]
        block_index = {b: i for i, b in enumerate(self.block_ids)}

        snaps = sorted(problem.snapshots, key=lambda s: (s.loss, s.snap_id))
        self.order_ids = [s.snap_id for s in snaps]
        self.losses = [s.loss for s in snaps]
        self.snap_blocks = [[block_index[b] for b in s.blocks] for s in snaps]

        self.live = [0] * len(self.block_ids)
        self.undecided = [0] * len(self.block_ids)
        for blocks in self.snap_blocks:
            for b in blocks:
                self.live[b] += 1
                self.undecided[b] += 1
        self.potential = sum(
            self.sizes[b] for b in range(len(self.block_ids)) if self.live[b] > 0
        )
        self.current: List[str] = []

    def _tick(self) -> None:
        self.nodes += 1
        if self.node_limit is not None and self.nodes > self.node_limit:
            raise _AbortSearch
        if self.time_limit is not None and \
                time.monotonic() - self.start > self.time_limit:
            raise _AbortSearch

    def _contribution(self, b: int) -> int:
        if self.live[b] > 0 and self.live[b] == self.undecided[b]:
            return self.sizes[b]
        return 0

    def _decide(self, pos: int) -> None:
        """Snapshot at pos leaves the undecided pool (both branches)."""
        for b in self.snap_blocks[pos]:
            self.potential -= self._contribution(b)
            self.undecided[b] -= 1
            self.potential += self._contribution(b)

    def _undecide(self, pos: int) -> None:
        for b in self.snap_blocks[pos]:
            self.potential -= self._contribution(b)
            self.undecided[b] += 1
            self.potential += self._contribution(b)

    def _include(self, pos: int) -> int:
        """Delete snapshot at pos; returns additional reclaimed bytes."""
        gained = 0
        for b in self.snap_blocks[pos]:
            self.potential -= self._contribution(b)
            self.live[b] -= 1
            if self.live[b] == 0:
                gained += self.sizes[b]
            self.potential += self._contribution(b)
        return gained

    def _uninclude(self, pos: int) -> None:
        for b in self.snap_blocks[pos]:
            self.potential -= self._contribution(b)
            self.live[b] += 1
            self.potential += self._contribution(b)


def solve_min_loss(problem: Problem, required_bytes: int,
                   node_limit: Optional[int] = None,
                   time_limit: Optional[float] = None) -> SearchResult:
    """Minimise loss subject to reclaiming >= required_bytes (exact)."""
    if required_bytes < 0:
        raise ValueError("required_bytes must be >= 0")
    search = _Search(problem, node_limit, time_limit)
    n = len(search.order_ids)

    best_loss: List[Optional[int]] = [None]
    best_count = [0]
    best_ids: List[tuple] = [()]
    best_gain = [0]

    def record(gain: int, loss: int) -> None:
        ids = tuple(sorted(search.current))
        key = (loss, len(ids), ids)
        if best_loss[0] is None or key < (best_loss[0], best_count[0], best_ids[0]):
            best_loss[0] = loss
            best_count[0] = len(ids)
            best_ids[0] = ids
            best_gain[0] = gain

    def dfs(pos: int, loss: int, gain: int) -> None:
        search._tick()
        if gain >= required_bytes:
            # Losses are non-negative, so excluding everything left is the
            # best completion of this prefix.
            record(gain, loss)
            return
        if gain + search.potential < required_bytes:
            return  # cannot reach the requirement from here
        if best_loss[0] is not None:
            if loss > best_loss[0]:
                return
            if loss == best_loss[0] and len(search.current) >= best_count[0]:
                return
        if pos == n:
            return
        search._decide(pos)
        # Include branch first: reaches feasibility quickly.
        gained = search._include(pos)
        search.current.append(search.order_ids[pos])
        dfs(pos + 1, loss + search.losses[pos], gain + gained)
        search.current.pop()
        search._uninclude(pos)
        # Exclude branch.
        dfs(pos + 1, loss, gain)
        search._undecide(pos)

    aborted = False
    old_limit = sys.getrecursionlimit()
    sys.setrecursionlimit(max(old_limit, n + 100))
    try:
        dfs(0, 0, 0)
    except _AbortSearch:
        aborted = True
    finally:
        sys.setrecursionlimit(old_limit)

    elapsed = time.monotonic() - search.start
    if best_loss[0] is None:
        return SearchResult(feasible=False, proven_optimal=not aborted,
                            nodes_explored=search.nodes,
                            elapsed_seconds=elapsed)
    return SearchResult(feasible=True, selected=list(best_ids[0]),
                        total_loss=best_loss[0],
                        reclaimed_bytes=best_gain[0],
                        proven_optimal=not aborted,
                        nodes_explored=search.nodes,
                        elapsed_seconds=elapsed)


def solve_max_reclaim(problem: Problem, loss_budget: int,
                      node_limit: Optional[int] = None,
                      time_limit: Optional[float] = None) -> SearchResult:
    """Maximise reclaimed bytes subject to total loss <= loss_budget."""
    if loss_budget < 0:
        raise ValueError("loss_budget must be >= 0")
    search = _Search(problem, node_limit, time_limit)
    n = len(search.order_ids)

    best_gain = [0]
    best_loss = [0]
    best_count = [0]
    best_ids: List[tuple] = [()]
    best_key: List[Optional[tuple]] = [None]

    def record(gain: int, loss: int) -> None:
        ids = tuple(sorted(search.current))
        key = (-gain, loss, len(ids), ids)
        if best_key[0] is None or key < best_key[0]:
            best_key[0] = key
            best_gain[0] = gain
            best_loss[0] = loss
            best_count[0] = len(ids)
            best_ids[0] = ids

    def dfs(pos: int, loss: int, gain: int) -> None:
        search._tick()
        if loss > loss_budget:
            return
        record(gain, loss)
        if gain + search.potential < best_gain[0]:
            return
        if gain + search.potential == best_gain[0] and loss >= best_loss[0] \
                and len(search.current) >= best_count[0]:
            return
        if pos == n:
            return
        search._decide(pos)
        gained = search._include(pos)
        search.current.append(search.order_ids[pos])
        dfs(pos + 1, loss + search.losses[pos], gain + gained)
        search.current.pop()
        search._uninclude(pos)
        dfs(pos + 1, loss, gain)
        search._undecide(pos)

    aborted = False
    old_limit = sys.getrecursionlimit()
    sys.setrecursionlimit(max(old_limit, n + 100))
    try:
        dfs(0, 0, 0)
    except _AbortSearch:
        aborted = True
    finally:
        sys.setrecursionlimit(old_limit)

    elapsed = time.monotonic() - search.start
    return SearchResult(feasible=True, selected=list(best_ids[0]),
                        total_loss=best_loss[0],
                        reclaimed_bytes=best_gain[0],
                        proven_optimal=not aborted,
                        nodes_explored=search.nodes,
                        elapsed_seconds=elapsed)
