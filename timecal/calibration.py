"""Rational affine calibration graphs between clocks.

A calibration is an exact affine map  y = scale*x + offset  plus an error
model  sum(coeff_i * eps_i)  with eps_i in [-1, 1].  Error terms are keyed
by a *source* name, so when the same calibration is composed with itself
(or its inverse) the correlated errors cancel exactly instead of widening
the interval the way naive interval arithmetic would.

No floats anywhere: scale/offset/coefficients are Fractions.
"""

from __future__ import annotations

import math
from collections import deque, namedtuple
from dataclasses import dataclass
from fractions import Fraction


def _merge_terms(terms):
    merged = {}
    for source, coeff in terms:
        coeff = Fraction(coeff)
        if coeff:
            merged[source] = merged.get(source, Fraction(0)) + coeff
    return tuple(sorted((s, c) for s, c in merged.items() if c))


@dataclass(frozen=True)
class Interval:
    lo: Fraction
    hi: Fraction

    def __post_init__(self):
        object.__setattr__(self, "lo", Fraction(self.lo))
        object.__setattr__(self, "hi", Fraction(self.hi))
        if self.lo > self.hi:
            raise ValueError("empty interval")

    @property
    def width(self) -> Fraction:
        return self.hi - self.lo

    def contains(self, x) -> bool:
        return self.lo <= Fraction(x) <= self.hi

    def __iter__(self):
        yield self.lo
        yield self.hi


@dataclass(frozen=True)
class AffineError:
    """center + sum(coeff_i * eps_i), eps_i in [-1, 1], correlated by source."""

    center: Fraction
    terms: tuple = ()

    def __post_init__(self):
        object.__setattr__(self, "center", Fraction(self.center))
        object.__setattr__(self, "terms", _merge_terms(self.terms))

    @property
    def radius(self) -> Fraction:
        return sum((abs(c) for _, c in self.terms), Fraction(0))

    def interval(self) -> Interval:
        r = self.radius
        return Interval(self.center - r, self.center + r)


@dataclass(frozen=True)
class CalibratedMap:
    """Exact affine map with a correlated error model."""

    scale: Fraction = Fraction(1)
    offset: Fraction = Fraction(0)
    terms: tuple = ()

    def __post_init__(self):
        object.__setattr__(self, "scale", Fraction(self.scale))
        object.__setattr__(self, "offset", Fraction(self.offset))
        object.__setattr__(self, "terms", _merge_terms(self.terms))
        if self.scale == 0:
            raise ValueError("affine scale must be non-zero")

    @staticmethod
    def identity() -> "CalibratedMap":
        return CalibratedMap()

    def with_error(self, source: str, magnitude) -> "CalibratedMap":
        return CalibratedMap(
            self.scale, self.offset, self.terms + ((source, Fraction(magnitude)),)
        )

    def then(self, nxt: "CalibratedMap") -> "CalibratedMap":
        """Compose: apply self first, then nxt."""
        return CalibratedMap(
            nxt.scale * self.scale,
            nxt.scale * self.offset + nxt.offset,
            tuple((s, c * nxt.scale) for s, c in self.terms) + nxt.terms,
        )

    def inverse(self) -> "CalibratedMap":
        inv = 1 / self.scale
        return CalibratedMap(
            inv, -self.offset * inv, tuple((s, -c * inv) for s, c in self.terms)
        )

    def apply(self, value) -> AffineError:
        return AffineError(self.scale * Fraction(value) + self.offset, self.terms)

    def apply_error(self, ae: AffineError) -> AffineError:
        return AffineError(
            self.scale * ae.center + self.offset,
            tuple((s, c * self.scale) for s, c in ae.terms) + self.terms,
        )

    def preimage(self, ye: AffineError) -> AffineError:
        """All x that could map into ye; correlated sources cancel."""
        return self.inverse().apply_error(ye)

    def is_identity(self) -> bool:
        return self.scale == 1 and self.offset == 0 and not self.terms


def integer_preimages(cal: CalibratedMap, y_lo, y_hi, include_error=True) -> range:
    """All integer x (e.g. sample ticks) whose image can fall in [y_lo, y_hi].

    The inverse of a monotone/quantized map is generally not unique, so the
    answer is a (possibly large) range of solutions, computed exactly.
    """
    lo, hi = Fraction(y_lo), Fraction(y_hi)
    if include_error:
        r = sum((abs(c) for _, c in cal.terms), Fraction(0))
        lo, hi = lo - r, hi + r
    a, b = cal.scale, cal.offset
    x1, x2 = (lo - b) / a, (hi - b) / a
    if a < 0:
        x1, x2 = x2, x1
    return range(math.ceil(x1), math.floor(x2) + 1)


_CacheEntry = namedtuple("_CacheEntry", ["cal", "deps"])


def _ukey(a: str, b: str):
    return (a, b) if a <= b else (b, a)


class NoPathError(LookupError):
    pass


@dataclass(frozen=True)
class Contradiction:
    """Evidence that composing calibrations around a cycle is not identity."""

    cycle: tuple            # node names, first == last
    composed: CalibratedMap

    @property
    def scale_residual(self) -> Fraction:
        return self.composed.scale - 1

    @property
    def offset_residual(self) -> Fraction:
        return self.composed.offset

    @property
    def error_interval(self) -> Interval:
        r = sum((abs(c) for _, c in self.composed.terms), Fraction(0))
        return Interval(-r, r)

    def describe(self) -> str:
        parts = [f"cycle {' -> '.join(self.cycle)}"]
        if self.scale_residual:
            parts.append(f"scale residual {self.scale_residual}")
        if self.offset_residual:
            parts.append(f"offset residual {self.offset_residual}s")
        ei = self.error_interval
        if ei.width:
            parts.append(f"residual error in [{ei.lo}, {ei.hi}]s")
        return "; ".join(parts)


@dataclass(frozen=True)
class PathDiscrepancy:
    path: tuple
    composed: CalibratedMap
    difference: CalibratedMap   # reference composed with path^-1


@dataclass(frozen=True)
class PathComparison:
    src: str
    dst: str
    reference_path: tuple
    reference: CalibratedMap
    discrepancies: tuple

    @property
    def consistent(self) -> bool:
        return not self.discrepancies


class _GraphVersion:
    __slots__ = ("number", "edges", "cache")

    def __init__(self, number, edges, cache):
        self.number = number
        self.edges = edges      # (src, dst) -> CalibratedMap
        self.cache = cache      # (src, dst) -> _CacheEntry


class GraphView:
    """Read-only view of one immutable version of a ClockGraph."""

    def __init__(self, gver: _GraphVersion):
        self._g = gver

    @property
    def version(self) -> int:
        return self._g.number

    def _adjacency(self):
        adj = {}
        for (u, v), cal in self._g.edges.items():
            adj.setdefault(u, []).append((v, cal, _ukey(u, v)))
            adj.setdefault(v, []).append((u, cal.inverse(), _ukey(u, v)))
        for entries in adj.values():
            entries.sort(key=lambda e: e[0])
        return adj

    def _bfs(self, src, dst, banned=None):
        adj = self._adjacency()
        prev = {src: None}
        queue = deque([src])
        while queue:
            u = queue.popleft()
            if u == dst:
                break
            for v, cal, ukey in adj.get(u, ()):
                if ukey == banned or v in prev:
                    continue
                prev[v] = (u, cal, ukey)
                queue.append(v)
        if dst not in prev:
            return None
        hops = []
        node = dst
        while node != src:
            u, cal, ukey = prev[node]
            hops.append((u, node, cal, ukey))
            node = u
        hops.reverse()
        return hops

    def map(self, src: str, dst: str) -> CalibratedMap:
        """Composed calibration along the shortest path; cached per version."""
        if src == dst:
            return CalibratedMap.identity()
        key = (src, dst)
        entry = self._g.cache.get(key)
        if entry is not None:
            return entry.cal
        hops = self._bfs(src, dst)
        if hops is None:
            raise NoPathError(f"no calibration path from {src!r} to {dst!r}")
        composed = CalibratedMap.identity()
        deps = []
        for _, _, cal, ukey in hops:
            composed = composed.then(cal)
            deps.append(ukey)
        self._g.cache[key] = _CacheEntry(composed, frozenset(deps))
        return composed

    def convert(self, src: str, dst: str, value) -> AffineError:
        return self.map(src, dst).apply(value)

    def check_cycles(self) -> tuple:
        """Compose around every cycle; report non-identity contradictions."""
        contradictions = []
        for (u, v), cal in sorted(self._g.edges.items()):
            key = _ukey(u, v)
            hops = self._bfs(v, u, banned=key)
            if hops is None:
                continue
            composed = cal
            nodes = [u, v]
            for _, to, c, _ in hops:
                composed = composed.then(c)
                nodes.append(to)
            if not composed.is_identity():
                contradictions.append(Contradiction(tuple(nodes), composed))
        return tuple(contradictions)

    def compare_paths(self, src: str, dst: str, max_paths=32, max_depth=16):
        """Compose every simple path src->dst and diff against the reference.

        The reference is the direct edge if one exists, else the shortest
        path.  Any path whose composition disagrees is reported with the
        exact difference map as evidence.
        """
        adj = self._adjacency()
        paths = []

        def dfs(node, nodes, composed):
            if len(paths) >= max_paths:
                return
            if node == dst:
                paths.append((tuple(nodes), composed))
                return
            if len(nodes) > max_depth:
                return
            for v, cal, _ in adj.get(node, ()):
                if v in nodes:
                    continue
                dfs(v, nodes + [v], composed.then(cal))

        dfs(src, [src], CalibratedMap.identity())
        if not paths:
            raise NoPathError(f"no calibration path from {src!r} to {dst!r}")
        direct = self._g.edges.get((src, dst))
        if direct is not None:
            ref_path, ref = (src, dst), direct
        else:
            ref_path, ref = min(paths, key=lambda p: len(p[0]))
        discrepancies = []
        for nodes, composed in paths:
            if nodes == tuple(ref_path):
                continue
            diff = composed.inverse().then(ref)  # reference after path^-1
            if not diff.is_identity():
                discrepancies.append(PathDiscrepancy(nodes, composed, diff))
        return PathComparison(src, dst, tuple(ref_path), ref, tuple(discrepancies))


class ClockGraph:
    """Versioned calibration graph with selective cache invalidation.

    set_calibration() creates a new version.  Cached compositions that
    depend on the corrected edge are dropped for the new version; every
    older version keeps its own edges and cache untouched.
    """

    def __init__(self):
        self._versions = [_GraphVersion(0, {}, {})]

    @property
    def version(self) -> int:
        return self._versions[-1].number

    def view(self) -> GraphView:
        return GraphView(self._versions[-1])

    def at_version(self, number: int) -> GraphView:
        if not 0 <= number <= self.version:
            raise ValueError(f"unknown version {number}")
        return GraphView(self._versions[number])

    def cache_keys(self, version=None) -> tuple:
        gver = self._versions[self.version if version is None else version]
        return tuple(sorted(gver.cache))

    def set_calibration(self, src: str, dst: str, cal: CalibratedMap) -> None:
        cur = self._versions[-1]
        edges = dict(cur.edges)
        # the latest call defines both orientation and map for the edge
        edges.pop((dst, src), None)
        edges[(src, dst)] = cal
        key = _ukey(src, dst)
        cache = {k: e for k, e in cur.cache.items() if key not in e.deps}
        self._versions.append(_GraphVersion(cur.number + 1, edges, cache))
