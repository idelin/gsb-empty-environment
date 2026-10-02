"""Layer-synchronous BFS model checker for finite-state models.

Design guarantees:

- Shortest counterexample: states are expanded in strict BFS layers and
  deduplication happens *synchronously* at discovery time, before the
  layer closes. No asynchronous/deferred dedup is used, so the first
  violation found is always at the minimal depth.
- Compression with recovery: canonicalization may merge concrete states,
  but the store keeps a recovery map so traces use concrete, replayable
  states.
- Collision safety: dedup compares full canonical states on hash hits.
- Budget exhaustion yields UNDECIDED (never SAFE) with coverage stats and
  evidence of the unexplored frontier.
- Layer-boundary checkpoints allow crash-free resume without re-expanding
  or skipping any state.
"""
from __future__ import annotations

import enum
import itertools
import os
import pickle
import shutil
import tempfile
import time
from dataclasses import dataclass, field

from frontier import TieredFrontier
from store import StateStore


class Status(enum.Enum):
    SAFE = "safe"            # fully explored, no violation
    VIOLATED = "violated"    # shortest counterexample found
    UNDECIDED = "undecided"  # budget exhausted before exploration finished


@dataclass
class Coverage:
    states_explored: int = 0       # states whose successors were expanded
    layers_completed: int = 0      # fully expanded BFS layers
    duplicates: int = 0            # discoveries dropped by dedup/compression
    collisions: int = 0            # hash collisions resolved by full compare
    spills: int = 0                # frontier items written to the disk tier
    frontier_size: int = 0         # unexplored states at return time
    frontier_evidence: list = field(default_factory=list)
    reason: str = ""               # why UNDECIDED, if applicable


@dataclass
class Result:
    status: Status
    trace: list | None             # concrete states, initial ... bad
    coverage: Coverage

    @property
    def trace_length(self) -> int | None:
        """Number of transitions; an initial-state violation has length 0."""
        return None if self.trace is None else len(self.trace) - 1


def replay_trace(model, trace) -> bool:
    """Verify a counterexample trace against the model, transition by transition."""
    if not trace:
        return False
    if not any(trace[0] == s0 for s0 in model.initial()):
        return False
    for a, b in zip(trace, trace[1:]):
        if not any(b == t for t in model.successors(a)):
            return False
    return bool(model.is_bad(trace[-1]))


class BfsChecker:
    def __init__(self, model, *, memory_limit: int = 4096,
                 disk_dir: str | None = None,
                 budget_states: int | None = None,
                 budget_layers: int | None = None,
                 deadline_s: float | None = None,
                 checkpoint_dir: str | None = None,
                 checkpoint_every: int = 1,
                 evidence_sample: int = 8) -> None:
        self.model = model
        self._canonicalize = getattr(model, "canonicalize", None) or (lambda s: s)
        self.memory_limit = memory_limit
        self.budget_states = budget_states
        self.budget_layers = budget_layers
        self.deadline_s = deadline_s
        self.checkpoint_dir = checkpoint_dir
        self.checkpoint_every = max(1, checkpoint_every)
        self.evidence_sample = evidence_sample

        self._owned_disk_dir = disk_dir is None
        self.disk_dir = disk_dir or tempfile.mkdtemp(prefix="bfs-frontier-")
        self._frontier_ids = itertools.count()

        self.store = StateStore()
        self.parents: dict = {}  # canonical -> parent canonical (None for roots)
        self.coverage = Coverage()
        self._total_spills = 0
        self._layer = 0
        self._resumed_frontier: list | None = None
        self._open_frontiers: list[TieredFrontier] = []

    # ------------------------------------------------------------------ run

    def run(self, resume: bool = False) -> Result:
        deadline = None if self.deadline_s is None else time.monotonic() + self.deadline_s
        try:
            frontier = self._new_frontier()
            if resume and self._checkpoint_exists():
                self._load_checkpoint()
                frontier.extend(self._resumed_frontier)
                self._resumed_frontier = None
            else:
                seed_trace = self._seed(frontier)
                if seed_trace is not None:
                    return self._finish(Status.VIOLATED, seed_trace, frontier)

            while frontier:
                if self.budget_layers is not None and self._layer >= self.budget_layers:
                    return self._undecided("layer budget exhausted", frontier, None)
                if self._past(deadline):
                    return self._undecided("deadline exceeded", frontier, None)

                nxt = self._new_frontier()
                while frontier:
                    if self.budget_states is not None and \
                            self.coverage.states_explored >= self.budget_states:
                        return self._undecided("state budget exhausted", frontier, nxt)
                    if self._past(deadline):
                        return self._undecided("deadline exceeded", frontier, nxt)

                    canonical, concrete = frontier.pop()
                    for succ in self.model.successors(concrete):
                        cs = self._canonicalize(succ)
                        if self.store.add_if_new(cs, succ):
                            self.parents[cs] = canonical
                            if self.model.is_bad(succ):
                                trace = self._trace(cs)
                                return self._finish(Status.VIOLATED, trace, frontier, nxt)
                            nxt.push((cs, succ))
                        else:
                            self.coverage.duplicates += 1
                    self.coverage.states_explored += 1

                self.coverage.layers_completed += 1
                self._layer += 1
                self._close_frontier(frontier)
                frontier = nxt
                self._maybe_checkpoint(frontier)

            return self._finish(Status.SAFE, None, frontier)
        finally:
            for fr in list(self._open_frontiers):
                self._close_frontier(fr)
            if self._owned_disk_dir:
                shutil.rmtree(self.disk_dir, ignore_errors=True)

    # -------------------------------------------------------------- helpers

    def _seed(self, frontier: TieredFrontier):
        """Dedup initial states; a bad initial state yields a length-0 trace."""
        for s0 in self.model.initial():
            c = self._canonicalize(s0)
            if self.store.add_if_new(c, s0):
                self.parents[c] = None
                if self.model.is_bad(s0):
                    return [s0]  # zero transitions
                frontier.push((c, s0))
        return None

    def _trace(self, canonical) -> list:
        seq = []
        c = canonical
        while c is not None:
            seq.append(self.store.recovery[c])
            c = self.parents[c]
        seq.reverse()
        return seq

    def _undecided(self, reason: str, *frontiers) -> Result:
        self.coverage.reason = reason
        evidence = []
        total = 0
        for offset, fr in enumerate(f for f in frontiers if f is not None):
            items = fr.drain()
            total += len(items)
            for canonical, _concrete in items:
                if len(evidence) >= self.evidence_sample:
                    break
                evidence.append({
                    "depth": self._layer + offset,
                    "hash": hash(canonical),
                    "state": repr(canonical),
                })
        self.coverage.frontier_size = total
        self.coverage.frontier_evidence = evidence
        return self._finish(Status.UNDECIDED, None)

    def _finish(self, status: Status, trace, *frontiers) -> Result:
        self.coverage.collisions = self.store.collisions
        self.coverage.spills = self._total_spills + sum(
            f.spills for f in self._open_frontiers)
        for fr in frontiers:
            if fr is not None:
                self._close_frontier(fr)
        return Result(status, trace, self.coverage)

    @staticmethod
    def _past(deadline) -> bool:
        return deadline is not None and time.monotonic() >= deadline

    def _new_frontier(self) -> TieredFrontier:
        path = os.path.join(self.disk_dir, f"frontier-{next(self._frontier_ids)}.sqlite")
        fr = TieredFrontier(self.memory_limit, path)
        self._open_frontiers.append(fr)
        return fr

    def _close_frontier(self, fr: TieredFrontier) -> None:
        self._total_spills += fr.spills
        fr.spills = 0
        fr.close()
        if fr in self._open_frontiers:
            self._open_frontiers.remove(fr)

    # ---------------------------------------------------------- checkpointing

    def _checkpoint_path(self) -> str:
        return os.path.join(self.checkpoint_dir, "checkpoint.pkl")

    def _checkpoint_exists(self) -> bool:
        return bool(self.checkpoint_dir) and os.path.exists(self._checkpoint_path())

    def _maybe_checkpoint(self, frontier: TieredFrontier) -> None:
        if not self.checkpoint_dir or self._layer % self.checkpoint_every != 0:
            return
        os.makedirs(self.checkpoint_dir, exist_ok=True)
        payload = {
            "layer": self._layer,
            "frontier": frontier.drain(),          # FIFO order preserved
            "store": self.store.snapshot(),
            "parents": self.parents,
            "coverage": self.coverage,
        }
        frontier.extend(payload["frontier"])       # restore after draining
        tmp = self._checkpoint_path() + ".tmp"
        with open(tmp, "wb") as fh:
            pickle.dump(payload, fh)
        os.replace(tmp, self._checkpoint_path())   # atomic rename

    def _load_checkpoint(self) -> None:
        with open(self._checkpoint_path(), "rb") as fh:
            payload = pickle.load(fh)
        self._layer = payload["layer"]
        self.store.restore(payload["store"])
        self.parents = payload["parents"]
        self.coverage = payload["coverage"]
        self._resumed_frontier = payload["frontier"]
