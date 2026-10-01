"""Calibration graph: rational affine calibrations between many clocks.

Features
--------
* Exact path composition (no floats anywhere).
* Cycle audit: compose around loops and check the result is the identity;
  contradictions are reported with numeric evidence.
* Multi-path comparison: direct mapping vs. composed detours.
* Interval propagation with correlated error sources.
* Versioned cache: correcting one calibration locally invalidates only the
  cache entries that depended on it; older versions stay in history.
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass, field
from fractions import Fraction

from .calibration import AffineCalibration
from .errorbudget import ErrorBudget

_MAX_PATHS = 16


@dataclass(frozen=True)
class CycleReport:
    """Evidence for one loop in the calibration graph."""

    clocks: tuple                 # cycle, e.g. ("A", "B", "C", "A")
    composed: AffineCalibration   # composition around the loop
    residual_slope: Fraction      # composed.slope - 1 (0 when consistent)
    residual_offset: Fraction     # composed.offset     (0 when consistent)

    @property
    def consistent(self) -> bool:
        return self.residual_slope == 0 and self.residual_offset == 0

    def describe(self) -> str:
        loop = " -> ".join(self.clocks)
        if self.consistent:
            return f"cycle {loop}: identity, consistent"
        slope = f"+{self.residual_slope}" if self.residual_slope >= 0 else str(self.residual_slope)
        offset = f"+{self.residual_offset}" if self.residual_offset >= 0 else str(self.residual_offset)
        return (
            f"cycle {loop}: CONTRADICTION, "
            f"residual slope {slope}, "
            f"residual offset {offset} s"
        )


@dataclass
class CacheEntry:
    deps: frozenset               # {(edge_key, edge_version), ...}
    path: tuple                   # edge keys used, in order
    calibration: AffineCalibration
    valid: bool = True


class CalibrationGraph:
    def __init__(self):
        self._edges: dict = {}            # (src, dst) -> AffineCalibration
        self._edge_versions: dict = {}    # (src, dst) -> int
        self._cache: dict = {}            # (src, dst) -> list[CacheEntry]

    # ---------------------------------------------------------- mutations
    def add_calibration(self, cal: AffineCalibration) -> None:
        """Add or correct a calibration; dependent cache entries go stale."""
        key = (cal.source, cal.target)
        self._edges[key] = cal
        self._edge_versions[key] = self._edge_versions.get(key, 0) + 1
        self._invalidate(key)

    def _invalidate(self, changed_key) -> None:
        for entries in self._cache.values():
            for entry in entries:
                if any(edge_key == changed_key for edge_key, _ in entry.deps):
                    entry.valid = False

    # ------------------------------------------------------------ queries
    def clocks(self) -> list:
        nodes = set()
        for src, dst in self._edges:
            nodes.add(src)
            nodes.add(dst)
        return sorted(nodes)

    def _neighbors(self, node):
        """(next_node, edge_key, calibration) traversing edges both ways."""
        for (src, dst), cal in self._edges.items():
            if src == node:
                yield dst, (src, dst), cal
            if dst == node:
                yield src, (src, dst), cal.inverse()

    def all_paths(self, src: str, dst: str) -> list:
        """All simple paths (as tuples of edge keys), shortest first."""
        found = []
        stack = [(src, (src,), ())]
        while stack and len(found) < _MAX_PATHS:
            node, nodes, keys = stack.pop()
            if node == dst:
                found.append(keys)
                continue
            for nxt, key, _ in self._neighbors(node):
                if nxt not in nodes:
                    stack.append((nxt, nodes + (nxt,), keys + (key,)))
        found.sort(key=len)
        return found

    def _compose(self, src: str, dst: str, keys) -> AffineCalibration:
        """Compose calibrations along ``keys`` from ``src`` to ``dst``."""
        composed = None
        node = src
        for key in keys:
            edge = self._edges[key]
            cal = edge if edge.source == node else edge.inverse()
            node = cal.target
            composed = cal if composed is None else composed.then(cal)
        if composed is None:  # src == dst
            composed = AffineCalibration(src, dst, Fraction(1), Fraction(0))
        return composed

    def _deps_of(self, keys) -> frozenset:
        return frozenset((key, self._edge_versions[key]) for key in keys)

    def _resolve(self, src: str, dst: str):
        """(composed calibration, edge keys) using the versioned cache."""
        if src == dst:
            return AffineCalibration(src, dst, Fraction(1), Fraction(0)), ()
        entries = self._cache.get((src, dst), [])
        for entry in entries:
            if entry.valid and all(
                self._edge_versions.get(k) == v for k, v in entry.deps
            ):
                return entry.calibration, entry.path
        paths = self.all_paths(src, dst)
        if not paths:
            raise KeyError(f"no calibration path from {src!r} to {dst!r}")
        keys = paths[0]
        composed = self._compose(src, dst, keys)
        entries.append(
            CacheEntry(self._deps_of(keys), keys, composed, valid=True)
        )
        self._cache[(src, dst)] = entries
        return composed, keys

    def calibration(self, src: str, dst: str) -> AffineCalibration:
        """Composed calibration src->dst, using the versioned cache."""
        return self._resolve(src, dst)[0]

    def cache_history(self, src: str, dst: str) -> list:
        """All cache entries (valid and superseded) for a clock pair."""
        return list(self._cache.get((src, dst), []))

    # ---------------------------------------------------------- mapping
    def convert(self, value, src: str, dst: str) -> Fraction:
        """Exact conversion of a rational source value."""
        return self.calibration(src, dst).map_value(value)

    def convert_budget(self, budget: ErrorBudget, src: str, dst: str) -> ErrorBudget:
        """Propagate an interval edge-by-edge, preserving correlation."""
        if src == dst:
            return budget
        _, keys = self._resolve(src, dst)
        out = budget
        node = src
        for key in keys:
            edge = self._edges[key]
            cal = edge if edge.source == node else edge.inverse()
            node = cal.target
            out = cal.map_budget(out)
        return out

    # ------------------------------------------------------------ audits
    def compare_paths(self, src: str, dst: str) -> list:
        """Composed calibration per simple path, for direct-vs-detour checks."""
        results = []
        for keys in self.all_paths(src, dst):
            results.append((keys, self._compose(src, dst, keys)))
        return results

    def audit(self) -> list:
        """Check every edge against an alternate route; report each loop."""
        reports = []
        seen = set()
        for key, direct in self._edges.items():
            src, dst = key
            saved = self._edges.pop(key)
            try:
                paths = self.all_paths(src, dst)
            finally:
                self._edges[key] = saved
            for keys in paths:
                alt = self._compose(src, dst, keys)
                cycle = alt.then(direct.inverse())
                loop = tuple(self._loop_nodes(src, keys)) + (src,)
                signature = frozenset(loop[:-1])
                if signature in seen:
                    continue
                seen.add(signature)
                reports.append(
                    CycleReport(
                        clocks=loop,
                        composed=cycle,
                        residual_slope=cycle.slope - 1,
                        residual_offset=cycle.offset,
                    )
                )
        return reports

    def _loop_nodes(self, src: str, keys) -> list:
        nodes = [src]
        node = src
        for key in keys:
            edge = self._edges[key]
            node = edge.target if edge.source == node else edge.source
            nodes.append(node)
        return nodes
