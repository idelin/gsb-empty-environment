"""Piecewise sequence-number -> wall-clock mapping.

The mapper never assumes a uniform absolute clock. Within one epoch, every
anchor constrains the feasible wall-clock interval of every sequence number
through the slope bounds; the feasible interval at a query point is the
intersection of all per-anchor implications. Because each implication is an
interval on a line, infeasibility is always witnessed by at most two anchors,
which yields the minimal anchor conflict set directly.

Late calibration: adding an anchor after estimates were issued does not
rewrite history. Re-querying produces a new ``Estimate`` revision; the
superseded estimate is kept as evidence of what was previously reported.
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .model import Anchor, SlopeModel
from .timeprims import Duration, TimeInterval, WallTime


class EpochBoundaryError(ValueError):
    """Raised when an operation would bridge two clock epochs."""


class NoAnchorsError(KeyError):
    """Raised when an epoch has no calibration anchors."""


class ConflictError(ValueError):
    """Raised when anchors make a query infeasible; carries the witness pair."""

    def __init__(self, message, anchors=()):
        super().__init__(message)
        self.anchors = tuple(anchors)


@dataclass(frozen=True)
class Conflict:
    first: Anchor
    second: Anchor
    reason: str


class Estimate:
    """One issued mapping result; revisions form a chain via ``supersedes``."""

    __slots__ = ("epoch", "seq", "interval", "version", "evidence",
                 "supersedes", "superseded")

    def __init__(self, epoch, seq, interval, version, evidence, supersedes):
        self.epoch = epoch
        self.seq = seq
        self.interval = interval
        self.version = version
        self.evidence = evidence          # labels of anchors used
        self.supersedes = supersedes      # previous Estimate or None
        self.superseded = False           # set True when a revision replaces it

    def __repr__(self):
        return (f"Estimate(epoch={self.epoch}, seq={self.seq}, "
                f"interval={self.interval!r}, version={self.version}, "
                f"superseded={self.superseded})")


class ClockMapper:
    def __init__(self, slope: SlopeModel):
        self.slope = slope
        self._anchors: dict[int, list[Anchor]] = {}
        self._conflicts: dict[int, list[Conflict]] = {}
        self._anomalous: set[Anchor] = set()
        self._estimates: dict[tuple[int, int], list[Estimate]] = {}
        self._version = 0

    # -- anchors ---------------------------------------------------------

    def add_anchor(self, anchor: Anchor) -> list[Conflict]:
        """Register a calibration anchor; returns any new conflicts found.

        Conflicting anchors are kept (flagged anomalous) so that evidence is
        never silently discarded.
        """
        new_conflicts: list[Conflict] = []
        if not anchor.self_consistent:
            new_conflicts.append(Conflict(anchor, anchor,
                                          "anchor interval is empty (lo > hi)"))
        for other in self._anchors.get(anchor.epoch, []):
            conflict = self._check_pair(anchor, other)
            if conflict is not None:
                new_conflicts.append(conflict)
        bucket = self._anchors.setdefault(anchor.epoch, [])
        bucket.append(anchor)
        bucket.sort(key=lambda a: (a.seq, a.wall_lo, a.wall_hi))
        if new_conflicts:
            self._conflicts.setdefault(anchor.epoch, []).extend(new_conflicts)
            # The incoming anchor is the suspect; previously accepted anchors
            # stay clean unless they were flagged when they arrived.
            self._anomalous.add(anchor)
        return new_conflicts

    def _check_pair(self, x: Anchor, y: Anchor) -> Conflict | None:
        a, b = (x, y) if (x.seq, x.wall_lo) <= (y.seq, y.wall_lo) else (y, x)
        if a.seq == b.seq:
            if a.wall_hi < b.wall_lo:
                return Conflict(a, b, "same sequence number, disjoint wall intervals")
            return None
        delta = b.seq - a.seq
        reach_lo = a.wall_lo + delta * self.slope.min_period
        reach_hi = a.wall_hi + delta * self.slope.max_period
        if b.wall_hi < reach_lo:
            return Conflict(a, b, "wall clock moved backwards beyond slope "
                                  "bounds (rollback)")
        if b.wall_lo > reach_hi:
            return Conflict(a, b, "wall clock advanced faster than slope "
                                  "bounds allow")
        return None

    def anchors(self, epoch: int) -> tuple[Anchor, ...]:
        return tuple(self._anchors.get(epoch, ()))

    def conflicts(self, epoch: int) -> tuple[Conflict, ...]:
        return tuple(self._conflicts.get(epoch, ()))

    def is_anomalous(self, anchor: Anchor) -> bool:
        return anchor in self._anomalous

    def minimal_conflict_set(self, epoch: int) -> tuple[Anchor, ...] | None:
        """Smallest subset of anchors that is already inconsistent.

        A self-inconsistent anchor is a singleton; otherwise, because
        feasibility is interval intersection on a line, some pair of anchors
        always witnesses the inconsistency, and a witnessing pair is minimal.
        """
        for anchor in self._anchors.get(epoch, []):
            if not anchor.self_consistent:
                return (anchor,)
        for conflict in self._conflicts.get(epoch, []):
            return (conflict.first, conflict.second)
        return None

    # -- mapping ---------------------------------------------------------

    def _bounds(self, epoch: int, seq: int):
        anchors = self._anchors.get(epoch)
        if not anchors:
            raise NoAnchorsError(epoch)
        lo = hi = None
        lo_src = hi_src = None
        for anchor in anchors:
            a_lo, a_hi = anchor.implies(seq, self.slope)
            if lo is None or a_lo > lo:
                lo, lo_src = a_lo, anchor
            if hi is None or a_hi < hi:
                hi, hi_src = a_hi, anchor
        return lo, hi, lo_src, hi_src

    def feasible_interval(self, epoch: int, seq: int) -> TimeInterval:
        """Intersection of all anchor implications; empty iff inconsistent."""
        lo, hi, _, _ = self._bounds(epoch, seq)
        return TimeInterval(WallTime(lo), WallTime(hi))

    def map_seq(self, epoch: int, seq: int) -> Estimate:
        """Issue an estimate for ``seq``; revises any previous estimate."""
        lo, hi, lo_src, hi_src = self._bounds(epoch, seq)
        if lo > hi:
            raise ConflictError(
                f"infeasible at epoch {epoch} seq {seq}: "
                f"{lo_src.name()} and {hi_src.name()} disagree",
                anchors=(lo_src, hi_src),
            )
        interval = TimeInterval(WallTime(lo), WallTime(hi))
        evidence = tuple(a.name() for a in self._anchors[epoch])
        history = self._estimates.setdefault((epoch, seq), [])
        previous = history[-1] if history else None
        if (previous is not None and not previous.superseded
                and previous.interval == interval
                and previous.evidence == evidence):
            return previous
        self._version += 1
        estimate = Estimate(epoch, seq, interval, self._version,
                            evidence, previous)
        if previous is not None:
            previous.superseded = True
        history.append(estimate)
        return estimate

    def duration_between(self, epoch_a: int, seq_a: int,
                         epoch_b: int, seq_b: int) -> tuple[Duration, Duration]:
        """Feasible duration range between two samples.

        Dropped samples still consume time: the gap depends only on the
        sequence-number distance, not on contiguity. Epochs cannot be
        bridged: a clock reset severs any duration across it.
        """
        if epoch_a != epoch_b:
            raise EpochBoundaryError(
                f"cannot compute a duration across epochs {epoch_a} and {epoch_b}")
        delta = abs(seq_b - seq_a)
        lo = delta * self.slope.min_period
        hi = delta * self.slope.max_period
        if self._anchors.get(epoch_a):
            ia = self.feasible_interval(epoch_a, seq_a)
            ib = self.feasible_interval(epoch_a, seq_b)
            if seq_a <= seq_b:
                lo = max(lo, ib.lo.seconds - ia.hi.seconds)
                hi = min(hi, ib.hi.seconds - ia.lo.seconds)
            else:
                lo = max(lo, ia.lo.seconds - ib.hi.seconds)
                hi = min(hi, ia.hi.seconds - ib.lo.seconds)
        return Duration(lo), Duration(hi)

    def estimates(self, epoch: int, seq: int) -> tuple[Estimate, ...]:
        """Full revision history for a query point, oldest first."""
        return tuple(self._estimates.get((epoch, seq), ()))
