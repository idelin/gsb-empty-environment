"""Multi-group editable polyline engine with exact nearest-pair queries.

Semantics (all exact, no epsilon):

* An ``Engine`` owns named groups; each group holds editable segments
  (polylines are just sequences of segment ids chosen by the caller).
  A zero-length segment is a point.

* ``query(a, b)`` computes the nearest pair of segments between groups
  ``a`` and ``b``; ``query_all()`` computes it over every pair of
  distinct groups.  Pairs listed in the exclusion relation are never
  considered.  Intra-group pairs are never considered (a polyline
  touches itself, which would trivialize the answer).

* ``radius`` is an exact distance threshold: only pairs with
  ``dist2 <= radius**2`` are eligible.  When nothing qualifies the
  result is empty (``dist2 is None``).

* All tied evidence at the minimal squared distance is reported.
  Evidence is deduplicated by *geometry*: if two segment pairs produce
  the same closest point pair (e.g. a polyline vertex shared by two
  adjacent segments), only one record is kept -- the one with the
  lexicographically smallest (group, segment, parameter) tuple.

* ``mode="all"`` reports parallel-equidistant continua as exact
  parameter intervals; ``mode="representative"`` collapses each
  continuum to its lexicographically smallest parameter pair (a stable
  representative).

* Every query reads an immutable version: either an explicit
  ``Snapshot`` (historical queries) or a private one that is released
  when the query finishes -- including when it is cancelled via a
  ``CancelToken`` (raising ``QueryCancelled``).
"""

from __future__ import annotations

import heapq
import itertools
from dataclasses import dataclass
from fractions import Fraction

from geometry import bbox_dist2, closest_params, eval_point, frac
from tree import (
    NodePool, count_nodes, iter_leaves, tree_delete, tree_find, tree_insert,
)

MODE_ALL = "all"
MODE_REPRESENTATIVE = "representative"


class QueryCancelled(Exception):
    """Raised inside a query when its CancelToken fires."""


class CancelToken:
    """Cooperative cancellation.  ``check()`` is polled by traversals."""

    def __init__(self):
        self.cancelled = False

    def cancel(self):
        self.cancelled = True

    def check(self):
        if self.cancelled:
            raise QueryCancelled("query cancelled")


@dataclass(frozen=True)
class Evidence:
    """One tied nearest-pair witness.

    ``param_a``/``param_b`` are exact parameter intervals ``(lo, hi)`` on
    the two segments (``lo == hi`` for a unique closest point).
    ``point_a``/``point_b`` are the corresponding exact geometric points
    ``(lo_point, hi_point)``.
    """

    group_a: str
    seg_a: int
    group_b: str
    seg_b: int
    dist2: Fraction
    param_a: tuple
    param_b: tuple
    point_a: tuple
    point_b: tuple


@dataclass(frozen=True)
class QueryResult:
    """``dist2`` is None (with empty evidence) when nothing qualifies."""

    dist2: object  # Fraction or None
    evidence: tuple

    @property
    def found(self) -> bool:
        return self.dist2 is not None


def build_evidence(group_a, seg_a, p0, p1, group_b, seg_b, q0, q1, minimizer):
    """Create an Evidence record from exact segment data and a Minimizer."""
    m = minimizer
    return Evidence(
        group_a=group_a, seg_a=seg_a, group_b=group_b, seg_b=seg_b,
        dist2=m.dist2,
        param_a=(m.s_lo, m.s_hi),
        param_b=(m.t_lo, m.t_hi),
        point_a=(eval_point(p0, p1, m.s_lo), eval_point(p0, p1, m.s_hi)),
        point_b=(eval_point(q0, q1, m.t_lo), eval_point(q0, q1, m.t_hi)),
    )


def finalize_evidence(evidence, mode):
    """Apply representative collapsing, geometric dedup and stable order.

    Shared by the tree traversal and the brute-force oracle so both
    implement identical semantics.
    """
    if mode == MODE_REPRESENTATIVE:
        collapsed = []
        for ev in evidence:
            collapsed.append(Evidence(
                group_a=ev.group_a, seg_a=ev.seg_a,
                group_b=ev.group_b, seg_b=ev.seg_b, dist2=ev.dist2,
                param_a=(ev.param_a[0], ev.param_a[0]),
                param_b=(ev.param_b[0], ev.param_b[0]),
                point_a=(ev.point_a[0], ev.point_a[0]),
                point_b=(ev.point_b[0], ev.point_b[0]),
            ))
        evidence = collapsed
    elif mode != MODE_ALL:
        raise ValueError(f"unknown mode: {mode!r}")

    # Dedup by exact geometry: the same closest point pair (or the same
    # minimiser interval) reported by different segment pairs is kept once.
    best_by_geometry = {}
    for ev in evidence:
        key = (ev.point_a, ev.point_b)
        order = (ev.group_a, ev.seg_a, ev.group_b, ev.seg_b,
                 ev.param_a, ev.param_b)
        current = best_by_geometry.get(key)
        if current is None:
            best_by_geometry[key] = (order, ev)
        else:
            if order < current[0]:
                best_by_geometry[key] = (order, ev)
    result = [ev for _, ev in best_by_geometry.values()]
    result.sort(key=lambda ev: (ev.group_a, ev.seg_a, ev.group_b, ev.seg_b,
                                ev.param_a, ev.param_b))
    return tuple(result)


class _Group:
    """A named set of editable segments plus its versioned tree root."""

    __slots__ = ("name", "root", "version")

    def __init__(self, name):
        self.name = name
        self.root = None
        self.version = 0


class Snapshot:
    """Pins immutable versions of every group for historical queries.

    The exclusion relation is captured as well, so a historical query
    sees a fully consistent read-only version of the world.  Holding a
    snapshot keeps the referenced nodes alive; ``close()`` releases
    them.  Usable as a context manager.
    """

    def __init__(self, engine):
        self._engine = engine
        self._roots = {name: g.root for name, g in engine._groups.items()}
        self.versions = {name: g.version for name, g in engine._groups.items()}
        self.exclusions = frozenset(engine._exclusions)
        self._closed = False

    def root(self, group_name):
        return self._roots[group_name]

    @property
    def closed(self):
        return self._closed

    def close(self):
        self._roots = None
        self._closed = True

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()
        return False


class Engine:
    def __init__(self):
        self.pool = NodePool()
        self._groups = {}
        self._exclusions = set()  # set of frozenset({a, b})

    # -- structure -----------------------------------------------------

    def add_group(self, name):
        if name in self._groups:
            raise ValueError(f"duplicate group {name!r}")
        self._groups[name] = _Group(name)

    def _group(self, name):
        return self._groups[name]  # KeyError is the defined behaviour

    def set_segment(self, group, seg_id, p0, p1):
        """Insert a segment or move an existing one (local repair only)."""
        g = self._group(group)
        p0 = (frac(p0[0]), frac(p0[1]))
        p1 = (frac(p1[0]), frac(p1[1]))
        g.root = tree_insert(self.pool, g.root, seg_id, p0, p1)
        g.version += 1

    def remove_segment(self, group, seg_id):
        g = self._group(group)
        g.root = tree_delete(self.pool, g.root, seg_id)
        g.version += 1

    def segment(self, group, seg_id):
        """Current endpoints ``(p0, p1)`` of a segment, or None."""
        leaf = tree_find(self._group(group).root, seg_id)
        return None if leaf is None else (leaf.p0, leaf.p1)

    def set_exclusion(self, a, b):
        if a == b:
            raise ValueError("cannot exclude a group from itself")
        self._exclusions.add(frozenset((a, b)))

    def clear_exclusion(self, a, b):
        self._exclusions.discard(frozenset((a, b)))

    def is_excluded(self, a, b):
        return frozenset((a, b)) in self._exclusions

    # -- versions ------------------------------------------------------

    def snapshot(self):
        return Snapshot(self)

    def node_count(self):
        """Nodes reachable from the current version of every group."""
        return sum(count_nodes(g.root) for g in self._groups.values())

    # -- queries -------------------------------------------------------

    def query(self, group_a, group_b, radius=None, mode=MODE_ALL,
              snapshot=None, cancel=None):
        """Nearest pair between two distinct groups."""
        if group_a == group_b:
            raise ValueError("intra-group queries are not supported")
        own = snapshot is None
        snap = self.snapshot() if own else snapshot
        try:
            if frozenset((group_a, group_b)) in snap.exclusions:
                return QueryResult(None, ())
            max_dist2 = None if radius is None else frac(radius) ** 2
            best, ev = _nearest_between(
                group_a, snap.root(group_a),
                group_b, snap.root(group_b),
                max_dist2, cancel,
            )
            if not ev:
                return QueryResult(None, ())
            return QueryResult(best, finalize_evidence(ev, mode))
        finally:
            if own:
                snap.close()

    def query_all(self, radius=None, mode=MODE_ALL, snapshot=None,
                  cancel=None):
        """Nearest pair over all non-excluded pairs of distinct groups."""
        own = snapshot is None
        snap = self.snapshot() if own else snapshot
        try:
            max_dist2 = None if radius is None else frac(radius) ** 2
            names = sorted(self._groups)
            best = max_dist2
            evidence = []
            for i, a in enumerate(names):
                for b in names[i + 1:]:
                    if frozenset((a, b)) in snap.exclusions:
                        continue
                    pair_best, pair_ev = _nearest_between(
                        a, snap.root(a), b, snap.root(b), best, cancel,
                    )
                    if not pair_ev:
                        continue
                    if best is None or pair_best < best:
                        best = pair_best
                        evidence = list(pair_ev)
                    elif pair_best == best:
                        evidence.extend(pair_ev)
            if not evidence:
                return QueryResult(None, ())
            return QueryResult(best, finalize_evidence(evidence, mode))
        finally:
            if own:
                snap.close()


def _nearest_between(name_a, root_a, name_b, root_b, max_dist2, cancel):
    """Best-first traversal over the two immutable trees.

    Returns ``(best_dist2, raw_evidence)``; ``best_dist2`` equals
    ``max_dist2`` when nothing within the threshold exists (evidence is
    then empty).  Ties at exactly the best distance are all explored
    (branches are pruned only when their lower bound is strictly
    greater).
    """
    if root_a is None or root_b is None:
        return max_dist2, []
    best = max_dist2
    evidence = []
    counter = itertools.count()
    heap = [(bbox_dist2(root_a.bbox, root_b.bbox), next(counter),
             root_a, root_b)]
    while heap:
        bound, _, na, nb = heapq.heappop(heap)
        if best is not None and bound > best:
            break
        if cancel is not None:
            cancel.check()
        if na.is_leaf and nb.is_leaf:
            m = closest_params(na.p0, na.p1, nb.p0, nb.p1)
            if best is None or m.dist2 < best:
                best = m.dist2
                evidence = [build_evidence(
                    name_a, na.seg_id, na.p0, na.p1,
                    name_b, nb.seg_id, nb.p0, nb.p1, m)]
            elif m.dist2 == best:
                evidence.append(build_evidence(
                    name_a, na.seg_id, na.p0, na.p1,
                    name_b, nb.seg_id, nb.p0, nb.p1, m))
            continue
        if nb.is_leaf or (not na.is_leaf and na.height >= nb.height):
            heapq.heappush(heap, (bbox_dist2(na.left.bbox, nb.bbox),
                                  next(counter), na.left, nb))
            heapq.heappush(heap, (bbox_dist2(na.right.bbox, nb.bbox),
                                  next(counter), na.right, nb))
        else:
            heapq.heappush(heap, (bbox_dist2(na.bbox, nb.left.bbox),
                                  next(counter), na, nb.left))
            heapq.heappush(heap, (bbox_dist2(na.bbox, nb.right.bbox),
                                  next(counter), na, nb.right))
    return best, evidence
