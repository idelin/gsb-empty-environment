"""Offset tables: piecewise-constant UTC offset with local revision support."""
from __future__ import annotations

from bisect import bisect_left, bisect_right
from fractions import Fraction

from .exact import as_fraction, utc_seconds, wall_seconds


class OffsetTable:
    """Piecewise-constant offset (seconds east of UTC) over UTC time.

    ``transitions`` is an iterable of ``(utc_seconds, offset_seconds)``; the
    new offset is effective from that UTC instant, inclusive. All arithmetic
    is exact (``Fraction``).
    """

    def __init__(self, base_offset=0, transitions=()):
        self.base = as_fraction(base_offset)
        merged = {}
        for at, offset in transitions:
            merged[utc_seconds(at)] = as_fraction(offset)
        self._times = sorted(merged)
        self._offsets = [merged[t] for t in self._times]

    @classmethod
    def fixed(cls, offset):
        return cls(offset)

    @property
    def transitions(self):
        return list(zip(self._times, self._offsets))

    def offset_at(self, utc_value) -> Fraction:
        t = utc_seconds(utc_value)
        i = bisect_right(self._times, t) - 1
        return self.base if i < 0 else self._offsets[i]

    def to_local(self, utc_value) -> Fraction:
        t = utc_seconds(utc_value)
        return t + self.offset_at(t)

    @property
    def min_offset(self) -> Fraction:
        return min([self.base, *self._offsets])

    @property
    def max_offset(self) -> Fraction:
        return max([self.base, *self._offsets])

    def transitions_between(self, start, end):
        """Transitions strictly inside the open UTC interval (start, end)."""
        a, b = utc_seconds(start), utc_seconds(end)
        lo = bisect_right(self._times, a)
        hi = bisect_left(self._times, b)
        return list(zip(self._times[lo:hi], self._offsets[lo:hi]))

    def revise(self, span_start, span_end, new_transitions):
        """Locally revise the table: drop existing transitions inside
        ``[span_start, span_end)`` and install ``new_transitions`` (each must
        lie inside the revised span). Transitions outside the span are kept."""
        a, b = utc_seconds(span_start), utc_seconds(span_end)
        if b < a:
            raise ValueError("revision span is empty or inverted")
        merged = {
            t: o for t, o in zip(self._times, self._offsets) if not (a <= t < b)
        }
        for at, offset in new_transitions:
            t = utc_seconds(at)
            if not (a <= t < b):
                raise ValueError("revision transition outside the revised span")
            merged[t] = as_fraction(offset)
        self._times = sorted(merged)
        self._offsets = [merged[t] for t in self._times]

    def preimage_local_interval(self, local_start, local_end):
        """All UTC intervals whose local reading falls in
        ``[local_start, local_end)``.

        Returns a sorted list of disjoint ``(utc_start, utc_end)`` pairs.
        There may be several when the offset table makes local time repeat,
        so callers must not assume a single contiguous preimage.
        """
        l0, l1 = wall_seconds(local_start), wall_seconds(local_end)
        if l1 <= l0:
            return []
        lo = l0 - self.max_offset
        hi = l1 - self.min_offset
        i0 = bisect_right(self._times, lo)
        i1 = bisect_left(self._times, hi)
        cuts = [lo, *self._times[i0:i1], hi]
        pieces = []
        for a, b in zip(cuts, cuts[1:]):
            off = self.offset_at(a)
            s = max(a, l0 - off)
            e = min(b, l1 - off)
            if s < e:
                pieces.append((s, e))
        merged = []
        for s, e in pieces:
            if merged and s <= merged[-1][1]:
                merged[-1] = (merged[-1][0], max(merged[-1][1], e))
            else:
                merged.append((s, e))
        return merged
