"""Multi-channel piecewise-linear table with dynamic nodes and snapshots.

Semantics
---------
* Endpoints are exact: querying exactly at a breakpoint returns that node's
  measurement interval; no extrapolation beyond the first/last breakpoint.
* Interpolation never crosses a breakpoint: an interior query only uses the
  two nodes of its own segment.  At a breakpoint the derivative is reported
  as the hull of the left and right segment derivative intervals.
* Every query returns evidence stamped with the table epoch.  Any effective
  mutation (insert / delete / calibrate) bumps the epoch, which revokes all
  previously issued evidence; re-querying produces fresh evidence.
* Snapshots capture immutable state: calibration updates never leak into a
  snapshot taken earlier.
"""

from __future__ import annotations

import bisect
import math
from dataclasses import dataclass

from .index import SegmentIndex
from .interval import Interval
from .model import (
    Measurement,
    Node,
    NoDataError,
    OutOfDomainError,
    UnknownChannelError,
)


@dataclass(frozen=True)
class ValueEvidence:
    """Result of a point query: value interval, derivative interval, sources."""

    epoch: int
    channel: str
    x: float
    value: Interval
    derivative: Interval
    at_breakpoint: bool
    left_derivative: "Interval | None"
    right_derivative: "Interval | None"
    sources: tuple  # 1 node at a breakpoint, else the 2 segment endpoints


@dataclass(frozen=True)
class InverseSolution:
    """One connected component of an inverse query: point or whole interval."""

    x_lo: float
    x_hi: float
    sources: tuple  # nodes covering the solution, endpoints first/last

    @property
    def is_interval(self) -> bool:
        return self.x_hi > self.x_lo

    def contains(self, x: float, tol: float = 0.0) -> bool:
        return self.x_lo - tol <= x <= self.x_hi + tol


@dataclass(frozen=True)
class InverseEvidence:
    """Result of an inverse query; may hold several solutions (non-monotone)
    or interval solutions (platforms / overlapping error bands)."""

    epoch: int
    channel: str
    target: Interval
    solutions: tuple


class _State:
    __slots__ = ("channels", "versions", "index", "epoch")

    def __init__(self, channels, versions, index, epoch):
        self.channels = channels
        self.versions = versions
        self.index = index
        self.epoch = epoch


def _active(state: _State, x: float) -> "Node | None":
    versions = state.versions.get(x)
    return versions[-1] if versions else None


def _require_channel(state: _State, channel: str) -> None:
    if channel not in state.channels:
        raise UnknownChannelError(f"unknown channel {channel!r}; declared: {state.channels!r}")


def _seg_derivative(m0: Measurement, m1: Measurement, x0: float, x1: float) -> Interval:
    s_lo = (m1.lo - m0.lo) / (x1 - x0)
    s_hi = (m1.hi - m0.hi) / (x1 - x0)
    return Interval(min(s_lo, s_hi), max(s_lo, s_hi))


def _evaluate(state: _State, channel: str, x: float) -> ValueEvidence:
    _require_channel(state, channel)
    ci = state.index.channel(channel)
    xs = ci.xs
    if not xs:
        raise NoDataError(f"channel {channel!r} has no data")
    if x < xs[0] or x > xs[-1]:
        raise OutOfDomainError(
            f"x={x!r} outside [{xs[0]!r}, {xs[-1]!r}] for channel {channel!r}")
    i = bisect.bisect_left(xs, x)
    if i < len(xs) and xs[i] == x:
        node = _active(state, xs[i])
        meas = node.values[channel]
        left_d = right_d = None
        if i > 0:
            n0 = _active(state, xs[i - 1])
            left_d = _seg_derivative(n0.values[channel], meas, xs[i - 1], xs[i])
        if i < len(xs) - 1:
            n1 = _active(state, xs[i + 1])
            right_d = _seg_derivative(meas, n1.values[channel], xs[i], xs[i + 1])
        if left_d is not None and right_d is not None:
            deriv = left_d.hull(right_d)
        elif left_d is not None:
            deriv = left_d
        elif right_d is not None:
            deriv = right_d
        else:
            deriv = Interval(-math.inf, math.inf)  # isolated node: unknown slope
        return ValueEvidence(state.epoch, channel, x, meas.interval(), deriv,
                             True, left_d, right_d, (node,))
    x0, x1 = xs[i - 1], xs[i]
    n0, n1 = _active(state, x0), _active(state, x1)
    m0, m1 = n0.values[channel], n1.values[channel]
    t = (x - x0) / (x1 - x0)
    lo = m0.lo + t * (m1.lo - m0.lo)
    hi = m0.hi + t * (m1.hi - m0.hi)
    deriv = _seg_derivative(m0, m1, x0, x1)
    return ValueEvidence(state.epoch, channel, x, Interval(lo, hi), deriv,
                         False, deriv, deriv, (n0, n1))


def _solve_le(x0: float, f0: float, x1: float, f1: float, limit: float):
    """All x with linear f through (x0, f0), (x1, f1) satisfying f(x) <= limit."""
    if f0 == f1:
        return (-math.inf, math.inf) if f0 <= limit else None
    xt = x0 + (limit - f0) * (x1 - x0) / (f1 - f0)
    return (-math.inf, xt) if f1 > f0 else (xt, math.inf)


def _segment_solve(x0, m0, x1, m1, t_lo, t_hi):
    """x in [x0, x1] with lo(x) <= t_hi and hi(x) >= t_lo, or None."""
    r1 = _solve_le(x0, m0.lo, x1, m1.lo, t_hi)
    if r1 is None:
        return None
    r2 = _solve_le(x0, -m0.hi, x1, -m1.hi, -t_lo)  # hi(x) >= t_lo
    if r2 is None:
        return None
    a = max(x0, r1[0], r2[0])
    b = min(x1, r1[1], r2[1])
    return (a, b) if a <= b else None


def _merge_append(parts: list, a: float, b: float, n0: Node, n1: Node) -> None:
    if parts and parts[-1][1] == a:  # exact shared boundary -> one component
        parts[-1][1] = b
        parts[-1][2].append(n1)
    else:
        parts.append([a, b, [n0, n1]])


def _invert(state: _State, channel: str, t_lo: float, t_hi: float) -> InverseEvidence:
    _require_channel(state, channel)
    ci = state.index.channel(channel)
    xs = ci.xs
    if not xs:
        raise NoDataError(f"channel {channel!r} has no data")
    parts: list = []
    if len(xs) == 1:
        node = _active(state, xs[0])
        meas = node.values[channel]
        if meas.lo <= t_hi and meas.hi >= t_lo:
            _merge_append(parts, xs[0], xs[0], node, node)
    for seg in ci.segs:
        if seg.bound_lo > t_hi or seg.bound_hi < t_lo:
            continue  # pruned by the index
        n0, n1 = _active(state, seg.x0), _active(state, seg.x1)
        sol = _segment_solve(seg.x0, n0.values[channel],
                             seg.x1, n1.values[channel], t_lo, t_hi)
        if sol is not None:
            _merge_append(parts, sol[0], sol[1], n0, n1)
    solutions = tuple(
        InverseSolution(a, b, tuple(nodes)) for a, b, nodes in parts
    )
    return InverseEvidence(state.epoch, channel, Interval(t_lo, t_hi), solutions)


class CurveTable:
    """Dynamic multi-channel piecewise-linear curve set."""

    def __init__(self, channels) -> None:
        channels = tuple(channels)
        if not channels:
            raise ValueError("at least one channel must be declared")
        if len(set(channels)) != len(channels):
            raise ValueError(f"duplicate channels: {channels!r}")
        self._channels = channels
        self._versions: dict = {}  # x -> tuple[Node, ...] sorted by order_key
        self._index = SegmentIndex()
        self._epoch = 0

    @property
    def channels(self) -> tuple:
        return self._channels

    @property
    def epoch(self) -> int:
        return self._epoch

    @property
    def index(self) -> SegmentIndex:
        return self._index

    def _state(self) -> _State:
        return _State(self._channels, self._versions, self._index, self._epoch)

    def _meas_lookup(self):
        def get_meas(x: float, channel: str) -> Measurement:
            node = _active(self._state(), x)
            meas = node.values.get(channel) if node else None
            if meas is None:
                raise NoDataError(f"no measurement for channel {channel!r} at x={x!r}")
            return meas
        return get_meas

    def _check_node(self, node: Node) -> None:
        for channel in node.values:
            if channel not in self._channels:
                raise UnknownChannelError(
                    f"node carries undeclared channel {channel!r}")

    def active_node(self, x: float) -> "Node | None":
        versions = self._versions.get(x)
        return versions[-1] if versions else None

    def breakpoints(self, channel: str) -> tuple:
        if channel not in self._channels:
            raise UnknownChannelError(f"unknown channel {channel!r}")
        return tuple(self._index.channel(channel).xs)

    # ------------------------------------------------------------------ mutation
    def insert(self, node: Node) -> bool:
        """Insert a node version.  Returns True iff the visible state changed.

        The explicit priority rule (higher priority, then higher version)
        decides which version is active at the coordinate.
        """
        self._check_node(node)
        old_versions = self._versions.get(node.x, ())
        old_active = old_versions[-1] if old_versions else None
        kept = [n for n in old_versions if n.order_key() != node.order_key()]
        kept.append(node)
        kept.sort(key=Node.order_key)
        new_active = kept[-1]
        self._versions[node.x] = tuple(kept)
        if old_active == new_active:
            return False  # losing version or identical re-insert: no visible change
        self._apply_active_change(node.x, old_active, new_active)
        self._epoch += 1
        return True

    def delete(self, x: float) -> bool:
        """Remove all versions at coordinate ``x``.  Returns True if removed."""
        versions = self._versions.pop(x, None)
        if versions is None:
            return False
        get_meas = self._meas_lookup()
        for channel in sorted(versions[-1].values):
            self._index.delete(channel, x, get_meas)
        self._epoch += 1
        return True

    def calibrate(self, x: float, channel: str, lo: float, hi: float,
                  version: int, priority: "int | None" = None) -> Node:
        """Replace one channel's measurement at ``x`` with a new version."""
        if channel not in self._channels:
            raise UnknownChannelError(f"unknown channel {channel!r}")
        active = self.active_node(x)
        if active is None:
            raise NoDataError(f"no node at x={x!r} to calibrate")
        values = dict(active.values)
        values[channel] = Measurement(lo, hi)
        node = Node(x=x, version=version,
                    priority=active.priority if priority is None else priority,
                    values=values)
        self.insert(node)
        return node

    def _apply_active_change(self, x: float, old: "Node | None", new: Node) -> None:
        get_meas = self._meas_lookup()
        old_channels = set(old.values) if old else set()
        new_channels = set(new.values)
        for channel in sorted(old_channels | new_channels):
            if channel in old_channels and channel in new_channels:
                if old.values[channel] != new.values[channel]:
                    self._index.insert(channel, x, get_meas)  # refresh adjacent
            elif channel in new_channels:
                self._index.insert(channel, x, get_meas)
            else:
                self._index.delete(channel, x, get_meas)

    # ------------------------------------------------------------------- queries
    def evaluate(self, channel: str, x: float) -> ValueEvidence:
        return _evaluate(self._state(), channel, x)

    def invert(self, channel: str, target) -> InverseEvidence:
        """Find all x whose value interval meets ``target`` (point or (lo, hi))."""
        t_lo, t_hi = (target, target) if isinstance(target, (int, float)) else target
        return _invert(self._state(), channel, t_lo, t_hi)

    def is_valid(self, evidence) -> bool:
        """Evidence is revoked as soon as the table changes effectively."""
        return evidence.epoch == self._epoch

    def snapshot(self) -> "Snapshot":
        return Snapshot(self)


class Snapshot:
    """Immutable view of a table at capture time; immune to later calibration."""

    def __init__(self, table: CurveTable) -> None:
        self._channels = table._channels
        self._versions = dict(table._versions)  # tuples of frozen Nodes: shared
        self._index = table._index.clone()
        self._epoch = table._epoch

    @property
    def epoch(self) -> int:
        return self._epoch

    def _state(self) -> _State:
        return _State(self._channels, self._versions, self._index, self._epoch)

    def active_node(self, x: float) -> "Node | None":
        versions = self._versions.get(x)
        return versions[-1] if versions else None

    def evaluate(self, channel: str, x: float) -> ValueEvidence:
        return _evaluate(self._state(), channel, x)

    def invert(self, channel: str, target) -> InverseEvidence:
        t_lo, t_hi = (target, target) if isinstance(target, (int, float)) else target
        return _invert(self._state(), channel, t_lo, t_hi)

    def is_valid(self, evidence) -> bool:
        return evidence.epoch == self._epoch
