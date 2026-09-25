"""Piecewise sample-index -> wall-clock mapping.

The mapper never assumes a uniform absolute clock.  Per epoch it maintains a
set of calibration anchors (interval-valued) plus slope (drift) bounds, and
derives:

* feasibility of the whole constraint system (difference constraints,
  checked with Bellman-Ford so contradictions are detected exactly);
* the feasible wall-clock band ``[lo(n), hi(n)]`` for any sample index --
  including indices that were dropped from the stream, because a dropped
  sample still occupies its nominal time interval;
* a synthetic offset conversion table: a piecewise list of segments, each
  carrying exact affine bound lines;
* on contradiction, a minimal (irreducible) conflict set of anchors.

Epochs are never bridged: constraints, bands and tables are computed per
epoch, and indices from different epochs are incomparable by construction.
Late-arriving calibrations revise the mapping of their epoch and produce an
evidence trail: previously reported results are kept, annotated with the
revised values.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from typing import Dict, List, Optional, Tuple

from .model import Anchor, Bound, Line, Segment, SlopeBounds
from .timetypes import Duration, WallClock

ZERO = Fraction(0)


# ---------------------------------------------------------------------------
# epoch-level constraint engine
# ---------------------------------------------------------------------------

class EpochMapper:
    """Constraint system and band computation for a single epoch."""

    def __init__(self, epoch: int, slope_bounds: SlopeBounds):
        self.epoch = epoch
        self.slope_bounds = slope_bounds
        self.anchors: List[Anchor] = []

    # -- feasibility -----------------------------------------------------

    def _edges(self, anchors: List[Anchor]):
        """Difference-constraint edges ``t_v - t_u <= w`` as (u, v, w).

        Node 0 is the absolute zero reference; node i>=1 is anchor i-1.
        """
        s_min = self.slope_bounds.min_slope
        s_max = self.slope_bounds.max_slope
        edges = []
        for pos, anchor in enumerate(anchors, start=1):
            edges.append((0, pos, anchor.latest.seconds))     # t_i <= hi
            edges.append((pos, 0, -anchor.earliest.seconds))  # t_i >= lo
        ordered = sorted(range(len(anchors)), key=lambda k: anchors[k].index)
        for left_pos in range(len(ordered)):
            for right_pos in range(left_pos + 1, len(ordered)):
                a = anchors[ordered[left_pos]]
                b = anchors[ordered[right_pos]]
                gap = b.index - a.index
                ia, ib = ordered[left_pos] + 1, ordered[right_pos] + 1
                edges.append((ia, ib, s_max * gap))   # t_b - t_a <= s_max*gap
                edges.append((ib, ia, -s_min * gap))  # t_a - t_b <= -s_min*gap
        return edges

    @staticmethod
    def _has_negative_cycle(n_nodes: int, edges) -> bool:
        # Bellman-Ford with a virtual super-source (all distances start at 0).
        dist = [ZERO] * n_nodes
        for _ in range(n_nodes - 1):
            changed = False
            for u, v, w in edges:
                if dist[v] > dist[u] + w:
                    dist[v] = dist[u] + w
                    changed = True
            if not changed:
                return False
        for u, v, w in edges:
            if dist[v] > dist[u] + w:
                return True
        return False

    def feasible(self, anchors: Optional[List[Anchor]] = None) -> bool:
        anchors = self.anchors if anchors is None else anchors
        if not anchors:
            return True
        return not self._has_negative_cycle(len(anchors) + 1, self._edges(anchors))

    def minimal_conflict_set(self) -> List[Anchor]:
        """An irreducible subset of anchors that is still infeasible.

        Deletion-based minimisation: every anchor in the returned set is
        necessary -- removing any one of them makes the rest feasible.
        Empty when the epoch is feasible.
        """
        if self.feasible():
            return []
        conflict = list(self.anchors)
        for anchor in list(conflict):
            trial = [a for a in conflict if a is not anchor]
            if trial and not self.feasible(trial):
                conflict = trial
        return sorted(conflict, key=lambda a: (a.index, a.earliest.seconds))

    # -- bands -------------------------------------------------------------

    def band_at(self, index: int) -> Optional[Tuple[Fraction, Fraction]]:
        """Feasible wall-clock band ``(lo, hi)`` in seconds for ``index``.

        ``lo > hi`` signals an infeasible constraint system.  Works for any
        index, including ones missing from the stream (dropped samples still
        span their nominal time interval).
        """
        if not self.anchors:
            return None
        s_min = self.slope_bounds.min_slope
        s_max = self.slope_bounds.max_slope
        lo = None
        hi = None
        for a in self.anchors:
            if index >= a.index:
                cand_lo = a.earliest.seconds + s_min * (index - a.index)
                cand_hi = a.latest.seconds + s_max * (index - a.index)
            else:
                cand_lo = a.earliest.seconds + s_max * (index - a.index)
                cand_hi = a.latest.seconds + s_min * (index - a.index)
            lo = cand_lo if lo is None else max(lo, cand_lo)
            hi = cand_hi if hi is None else min(hi, cand_hi)
        return lo, hi

    # -- synthetic offset conversion table --------------------------------

    def conversion_table(self) -> List[Segment]:
        """Piecewise conversion table for this epoch.

        Segments are split at anchor indices; each segment carries the exact
        affine bound lines (slope + offset) whose max/min give the feasible
        band.  This is the "synthetic offset" form: time = slope*index +
        offset, with per-segment slope/offset choices.
        """
        if not self.anchors:
            return []
        s_min = self.slope_bounds.min_slope
        s_max = self.slope_bounds.max_slope
        points = sorted({a.index for a in self.anchors})
        ranges: List[Tuple[Optional[int], Optional[int]]] = [(None, points[0])]
        for left, right in zip(points, points[1:]):
            ranges.append((left, right))
        ranges.append((points[-1], None))

        segments = []
        for first, last in ranges:
            lower_lines = []
            upper_lines = []
            for a in self.anchors:
                # On this segment, is n >= a.index throughout?
                after = first is not None and first >= a.index
                lo_slope = s_min if after else s_max
                hi_slope = s_max if after else s_min
                lower_lines.append(Line(lo_slope, a.earliest.seconds - lo_slope * a.index))
                upper_lines.append(Line(hi_slope, a.latest.seconds - hi_slope * a.index))
            segments.append(Segment(
                epoch=self.epoch,
                first_index=first,
                last_index=last,
                lower=Bound("max", _dedupe(lower_lines, keep="max")),
                upper=Bound("min", _dedupe(upper_lines, keep="min")),
            ))
        return segments


def _dedupe(lines: List[Line], keep: str) -> Tuple[Line, ...]:
    """Among parallel lines keep only the dominating one."""
    best: Dict[Fraction, Line] = {}
    for line in lines:
        current = best.get(line.slope)
        if current is None:
            best[line.slope] = line
        elif keep == "max" and line.offset > current.offset:
            best[line.slope] = line
        elif keep == "min" and line.offset < current.offset:
            best[line.slope] = line
    return tuple(best[slope] for slope in sorted(best))


# ---------------------------------------------------------------------------
# multi-epoch mapper with revision history and evidence
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class TimeInterval:
    """Result of mapping one sample index: an exact wall-clock interval."""

    epoch: int
    index: int
    earliest: WallClock
    latest: WallClock
    feasible: bool = True

    @property
    def width(self) -> Duration:
        return self.latest - self.earliest


@dataclass
class QueryRecord:
    """Evidence for a previously reported mapping result."""

    version: int
    epoch: int
    index: int
    interval: TimeInterval
    superseded: bool = False
    replaced_by: Optional[TimeInterval] = None


@dataclass
class Revision:
    """One mapping revision, triggered by a (possibly late) calibration."""

    version: int
    anchor: Anchor
    feasible: bool
    conflict_set: List[Anchor] = field(default_factory=list)
    rollback_detected: bool = False
    new_epoch: bool = False
    updates: List[Tuple[QueryRecord, TimeInterval]] = field(default_factory=list)


class PiecewiseClockMapper:
    """Maps (epoch, sample index) to feasible wall-clock intervals."""

    def __init__(self, nominal_rate, slope_bounds: Optional[SlopeBounds] = None):
        self.nominal_rate = Fraction(nominal_rate)
        if self.nominal_rate <= 0:
            raise ValueError("nominal_rate must be positive")
        self.slope_bounds = slope_bounds or SlopeBounds.from_nominal_rate(self.nominal_rate)
        self.epochs: Dict[int, EpochMapper] = {}
        self.version = 0
        self.revisions: List[Revision] = []
        self.evidence: List[QueryRecord] = []

    # -- anchors ---------------------------------------------------------

    def add_anchor(self, anchor: Anchor) -> Revision:
        """Add a calibration point (possibly late) and revise the mapping.

        Only the anchor's own epoch is recomputed; prior query evidence for
        that epoch is re-evaluated and annotated, never deleted.
        """
        new_epoch = anchor.epoch not in self.epochs
        rollback = False
        if new_epoch and self.epochs:
            previous = self.epochs[max(self.epochs)]
            last_index = max(a.index for a in previous.anchors)
            old_band = previous.band_at(last_index)
            if old_band is not None and anchor.latest.seconds < old_band[0]:
                rollback = True
        mapper = self.epochs.setdefault(anchor.epoch, EpochMapper(anchor.epoch, self.slope_bounds))
        mapper.anchors.append(anchor)
        self.version += 1

        feasible = mapper.feasible()
        conflict = [] if feasible else mapper.minimal_conflict_set()

        updates: List[Tuple[QueryRecord, TimeInterval]] = []
        for record in self.evidence:
            if record.epoch != anchor.epoch or record.superseded:
                continue
            revised = self._map(record.epoch, record.index)
            if revised != record.interval:
                record.superseded = True
                record.replaced_by = revised
                updates.append((record, revised))

        revision = Revision(
            version=self.version,
            anchor=anchor,
            feasible=feasible,
            conflict_set=conflict,
            rollback_detected=rollback,
            new_epoch=new_epoch,
            updates=updates,
        )
        self.revisions.append(revision)
        return revision

    # -- queries -----------------------------------------------------------

    def _map(self, epoch: int, index: int) -> TimeInterval:
        mapper = self.epochs[epoch]
        band = mapper.band_at(index)
        if band is None:
            raise KeyError(f"epoch {epoch} has no anchors yet")
        lo, hi = band
        return TimeInterval(epoch, index, WallClock(lo), WallClock(hi), feasible=lo <= hi)

    def map_index(self, epoch: int, index: int) -> TimeInterval:
        """Map a sample index of an epoch to its feasible wall-clock interval.

        The result is logged as evidence so that later (late) calibrations
        can revise it with a full audit trail.
        """
        if epoch not in self.epochs:
            raise KeyError(f"unknown epoch {epoch}; epochs are never bridged")
        interval = self._map(epoch, index)
        self.evidence.append(QueryRecord(self.version, epoch, index, interval))
        return interval

    def status(self, epoch: int) -> str:
        """``"ok"`` or ``"anomaly"`` (calibration points are inconsistent)."""
        mapper = self.epochs.get(epoch)
        if mapper is None:
            raise KeyError(f"unknown epoch {epoch}")
        return "ok" if mapper.feasible() else "anomaly"

    def conversion_table(self, epoch: int) -> List[Segment]:
        """The synthetic offset conversion table of one epoch."""
        mapper = self.epochs.get(epoch)
        if mapper is None:
            raise KeyError(f"unknown epoch {epoch}")
        return mapper.conversion_table()

    def conflict_set(self, epoch: int) -> List[Anchor]:
        """Minimal anchor conflict set for an epoch (empty when feasible)."""
        mapper = self.epochs.get(epoch)
        if mapper is None:
            raise KeyError(f"unknown epoch {epoch}")
        return mapper.minimal_conflict_set()


def find_drops(indices) -> List[Tuple[int, int, int]]:
    """Find dropped-sample gaps in an observed index sequence.

    Returns ``(first_missing, last_missing, count)`` triples.  A jump of 10
    (e.g. 41 followed by 51) means 9 samples were dropped but the time
    interval of 10 nominal periods still exists in the mapping.
    """
    ordered = sorted(indices)
    drops = []
    for prev, nxt in zip(ordered, ordered[1:]):
        if nxt > prev + 1:
            drops.append((prev + 1, nxt - 1, nxt - prev - 1))
    return drops
