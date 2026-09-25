"""Synthetic offset conversion table between sequence numbers and wall clock.

Each control point stores the exact rational offset interval
``offset = wall - seq * nominal_period``. Conversion interpolates offsets
linearly between control points using ``Fraction`` arithmetic only, so the
table is exact: no value is ever rounded to float.
"""
from __future__ import annotations

from fractions import Fraction

from .timeprims import TimeInterval, WallTime, exact


class OffsetTable:
    def __init__(self, nominal_period):
        self.nominal_period = exact(nominal_period)
        self._entries: dict[int, list[tuple[int, Fraction, Fraction]]] = {}

    def add(self, epoch: int, seq: int, wall_lo, wall_hi) -> None:
        off_lo = exact(wall_lo) - seq * self.nominal_period
        off_hi = exact(wall_hi) - seq * self.nominal_period
        entries = self._entries.setdefault(epoch, [])
        entries.append((seq, off_lo, off_hi))
        entries.sort(key=lambda entry: entry[0])

    @classmethod
    def from_mapper(cls, mapper, epoch: int) -> "OffsetTable":
        table = cls(mapper.slope.nominal_period)
        for anchor in mapper.anchors(epoch):
            table.add(epoch, anchor.seq, anchor.wall_lo, anchor.wall_hi)
        return table

    def entries(self, epoch: int) -> tuple:
        return tuple(self._entries.get(epoch, ()))

    def _bracket(self, epoch: int, seq: int):
        entries = self._entries[epoch]
        if not entries:
            raise KeyError(f"no control points for epoch {epoch}")
        if seq <= entries[0][0]:
            return entries[0], entries[0]
        if seq >= entries[-1][0]:
            return entries[-1], entries[-1]
        for e0, e1 in zip(entries, entries[1:]):
            if e0[0] <= seq <= e1[0]:
                return e0, e1
        raise AssertionError("unreachable")

    def to_wall(self, epoch: int, seq: int) -> TimeInterval:
        """Convert a sequence number to an exact wall-clock interval."""
        (s0, lo0, hi0), (s1, lo1, hi1) = self._bracket(epoch, seq)
        if s1 == s0:
            off_lo, off_hi = lo0, hi0
        else:
            t = Fraction(seq - s0, s1 - s0)
            off_lo = lo0 + t * (lo1 - lo0)
            off_hi = hi0 + t * (hi1 - hi0)
        base = seq * self.nominal_period
        return TimeInterval(WallTime(base + off_lo), WallTime(base + off_hi))

    def to_seq(self, epoch: int, wall) -> tuple[Fraction, Fraction]:
        """Convert a wall-clock point back to an exact sequence-number range."""
        w = wall.seconds if isinstance(wall, WallTime) else exact(wall)
        entries = self._entries[epoch]
        if not entries:
            raise KeyError(f"no control points for epoch {epoch}")
        period = self.nominal_period
        seq_lo = seq_hi = None

        def solve(seq0, off0, seq1, off1):
            # wall(seq) = seq*period + off0 + (seq-seq0)*(off1-off0)/(seq1-seq0)
            slope = Fraction(off1 - off0, seq1 - seq0)
            denom = period + slope
            if denom == 0:
                return None
            return Fraction(w - off0 + seq0 * slope, 1) / denom

        spans = list(zip(entries, entries[1:])) or [(entries[0], entries[0])]
        for (s0, lo0, hi0), (s1, lo1, hi1) in spans:
            wall_lo_end = s0 * period + lo0
            wall_hi_end = s1 * period + hi1
            if s0 == s1:
                if wall_lo_end <= w <= s0 * period + hi0:
                    seq_lo = seq_hi = Fraction(s0)
                continue
            if not (wall_lo_end <= w <= wall_hi_end):
                continue
            cand_hi = solve(s0, lo0, s1, lo1)  # w on the lower offset curve
            cand_lo = solve(s0, hi0, s1, hi1)  # w on the upper offset curve
            if cand_lo is None or cand_hi is None:
                continue
            cand_lo = max(Fraction(s0), min(Fraction(s1), cand_lo))
            cand_hi = max(Fraction(s0), min(Fraction(s1), cand_hi))
            lo_c, hi_c = (cand_lo, cand_hi) if cand_lo <= cand_hi else (cand_hi, cand_lo)
            seq_lo = lo_c if seq_lo is None else min(seq_lo, lo_c)
            seq_hi = hi_c if seq_hi is None else max(seq_hi, hi_c)
        if seq_lo is None:
            raise ValueError(f"wall time {w} is outside the table for epoch {epoch}")
        return seq_lo, seq_hi
