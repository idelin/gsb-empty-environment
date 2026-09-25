"""Versioned directed multigraph with walk counting and k-th selection.

Every mutation commits a new immutable version.  DP blocks (matrix
powers / prefix sums) are cached per (version, modulus); a mutation
therefore invalidates stale blocks incrementally, since the new version
rebuilds only what it needs, while every old version keeps its own
snapshot and caches, so historical answers stay available.
"""

from __future__ import annotations

from typing import Dict, Iterable, Optional, Tuple

from .analysis import find_cycle_evidence, relevant_slice
from .engine import CountEngine
from .graph_data import Edge, Snapshot
from .kth import kth_walk, rank_walk
from .results import CountResult, WalkResult


class VersionedGraph:
    def __init__(self) -> None:
        self._vertices = set()
        self._edges: Dict[str, Edge] = {}
        self._version = 0
        self._snapshots: Dict[int, Snapshot] = {0: Snapshot(0, frozenset(), ())}
        self._engines: Dict[Tuple[int, Optional[int]], CountEngine] = {}

    @property
    def version(self) -> int:
        return self._version

    def snapshot(self, version: Optional[int] = None) -> Snapshot:
        v = self._version if version is None else version
        try:
            return self._snapshots[v]
        except KeyError:
            raise ValueError(f"unknown version {v}") from None

    def _commit(self) -> int:
        self._version += 1
        self._snapshots[self._version] = Snapshot(
            self._version,
            frozenset(self._vertices),
            tuple(sorted(self._edges.values(), key=lambda e: e.eid)),
        )
        # Incremental invalidation: engines are keyed by (version, modulus),
        # so blocks belonging to superseded versions can never be reused for
        # the new state; they are dropped here while the snapshots themselves
        # remain queryable (old versions are preserved).
        stale = [key for key in self._engines if key[0] >= self._version]
        for key in stale:
            del self._engines[key]
        return self._version

    def add_vertex(self, vid: str) -> int:
        if vid in self._vertices:
            return self._version
        self._vertices.add(vid)
        return self._commit()

    def add_edge(self, eid: str, src: str, dst: str) -> int:
        if eid in self._edges:
            raise ValueError(f"duplicate edge id {eid!r}")
        self._vertices.update((src, dst))
        self._edges[eid] = Edge(eid, src, dst)
        return self._commit()

    def remove_edge(self, eid: str) -> int:
        del self._edges[eid]  # KeyError if absent
        return self._commit()

    def remove_edges(self, eids: Iterable[str]) -> int:
        """Batch removal committed as a single new version."""
        eids = list(eids)
        missing = [e for e in eids if e not in self._edges]
        if missing:
            raise KeyError(f"unknown edge ids: {missing}")
        for eid in eids:
            del self._edges[eid]
        return self._commit()

    def remove_vertex(self, vid: str) -> int:
        if vid not in self._vertices:
            raise KeyError(vid)
        self._vertices.discard(vid)
        self._edges = {
            i: e for i, e in self._edges.items() if e.src != vid and e.dst != vid
        }
        return self._commit()

    def _engine(self, version: int, modulus: Optional[int]) -> CountEngine:
        key = (version, modulus)
        eng = self._engines.get(key)
        if eng is None:
            eng = CountEngine(self._snapshots[version], modulus)
            self._engines[key] = eng
        return eng

    def count(
        self,
        source: str,
        target: str,
        lo: int = 0,
        hi: Optional[int] = None,
        *,
        modulus: Optional[int] = None,
        version: Optional[int] = None,
    ) -> CountResult:
        """Count source->target walks with length in [lo, hi).

        hi=None is the unbounded ("infinite length") mode: if a cycle is
        reachable from source and can lead to target, the result is
        infinite and carries cycle evidence; otherwise the exact finite
        count is returned (walks are then simple, so length < #vertices).
        """
        if lo < 0:
            raise ValueError("lo must be >= 0")
        if hi is not None and hi < 0:
            raise ValueError("hi must be >= 0 or None")
        snap = self.snapshot(version)
        slice_ = relevant_slice(snap, source, target)
        if hi is None:
            evidence = find_cycle_evidence(snap, source, target)
            if evidence is not None:
                return CountResult(
                    version=snap.version, source=source, target=target,
                    lo=lo, hi=None, modulus=modulus, count=None,
                    infinite=True, evidence=evidence, reachable_slice=slice_,
                )
            hi_eff = max(lo, len(snap.vertices))
        else:
            evidence = None
            hi_eff = hi
        engine = self._engine(snap.version, modulus)
        c = engine.count_range(source, target, lo, hi_eff) if hi_eff > lo else 0
        return CountResult(
            version=snap.version, source=source, target=target,
            lo=lo, hi=hi, modulus=modulus, count=c,
            infinite=False, evidence=None, reachable_slice=slice_,
        )

    def kth(
        self,
        source: str,
        target: str,
        lo: int,
        hi: Optional[int],
        k: int,
        *,
        max_length: int = 10_000,
        budget: int = 1_000_000,
        version: Optional[int] = None,
        modulus: Optional[int] = None,
    ) -> WalkResult:
        """k-th (0-based) walk in lexicographic edge-id order.

        max_length caps the materialized trajectory so huge intervals
        cannot exhaust memory; budget caps the selection work and raises
        BudgetExhausted (never a false "no path") when it runs out.
        """
        if modulus is not None:
            raise ValueError(
                "k-th selection requires exact counts; modular counts "
                "must not be mixed with ordering/selection"
            )
        if lo < 0:
            raise ValueError("lo must be >= 0")
        snap = self.snapshot(version)
        if hi is None:
            if find_cycle_evidence(snap, source, target) is not None:
                raise ValueError(
                    "unbounded interval with infinitely many walks; "
                    "pass a finite hi"
                )
            hi = max(lo, len(snap.vertices))
        engine = self._engine(snap.version, None)
        return kth_walk(
            snap, engine, source, target, lo, hi, k,
            max_length=max_length, budget=budget,
        )

    def rank(
        self,
        source: str,
        target: str,
        lo: int,
        hi: int,
        edge_ids: Iterable[str],
        *,
        version: Optional[int] = None,
    ) -> int:
        """Inverse of kth(): lexicographic rank of a concrete walk."""
        snap = self.snapshot(version)
        engine = self._engine(snap.version, None)
        return rank_walk(snap, engine, source, target, lo, hi, tuple(edge_ids))
