"""Exact time axis primitives.

Three distinct concepts are modeled as three distinct types:

- ``Duration``:  a length of time (exact rational number of seconds).
- ``Instant``:   a point on the abstract timeline (offset from the timeline
                 epoch).  This is *the* time point type.
- ``WallClock``: a wall-clock reading.  It is *not* a timeline point; it only
                 becomes one through an :class:`OffsetTable`.

All arithmetic is exact, backed by :class:`fractions.Fraction`.  Illegal
mixing (e.g. ``Instant + WallClock`` or ``Instant + Instant``) raises
``TypeError``.
"""

from __future__ import annotations

from bisect import bisect_right
from fractions import Fraction
from typing import Iterable, Tuple, Union


def _frac(value: Union[int, float, str, Fraction, "Duration"]) -> Fraction:
    if isinstance(value, Fraction):
        return value
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, str):
        return Fraction(value)
    if isinstance(value, float):
        # Exact decimal conversion of the float, no silent rounding.
        return Fraction(value)
    raise TypeError(f"cannot interpret {value!r} as an exact time quantity")


class Duration:
    """An exact length of time, in seconds."""

    __slots__ = ("_seconds",)

    def __init__(self, seconds: Union[int, float, str, Fraction] = 0):
        object.__setattr__(self, "_seconds", _frac(seconds))

    @property
    def seconds(self) -> Fraction:
        return self._seconds

    @classmethod
    def seconds_(cls, n) -> "Duration":
        return cls(n)

    @classmethod
    def minutes(cls, n) -> "Duration":
        return cls(_frac(n) * 60)

    @classmethod
    def hours(cls, n) -> "Duration":
        return cls(_frac(n) * 3600)

    def __add__(self, other):
        if isinstance(other, Duration):
            return Duration(self._seconds + other._seconds)
        if isinstance(other, (Instant, WallClock)):
            return other + self
        return NotImplemented

    def __radd__(self, other):
        if isinstance(other, (Instant, WallClock)):
            return other + self
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Duration(self._seconds - other._seconds)
        return NotImplemented

    def __neg__(self):
        return Duration(-self._seconds)

    def __mul__(self, other):
        if isinstance(other, (int, Fraction)):
            return Duration(self._seconds * other)
        return NotImplemented

    __rmul__ = __mul__

    def __truediv__(self, other):
        if isinstance(other, (int, Fraction)):
            return Duration(self._seconds / other)
        if isinstance(other, Duration):
            return self._seconds / other._seconds
        return NotImplemented

    def __eq__(self, other):
        return isinstance(other, Duration) and self._seconds == other._seconds

    def __lt__(self, other):
        if not isinstance(other, Duration):
            return NotImplemented
        return self._seconds < other._seconds

    def __le__(self, other):
        if not isinstance(other, Duration):
            return NotImplemented
        return self._seconds <= other._seconds

    def __hash__(self):
        return hash(("Duration", self._seconds))

    def __repr__(self):
        return f"Duration({self._seconds})"


class _Point:
    """Shared behaviour for timeline points and wall-clock readings."""

    __slots__ = ("_offset",)

    def __init__(self, offset: Union[int, float, str, Fraction]):
        object.__setattr__(self, "_offset", _frac(offset))

    @property
    def offset(self) -> Fraction:
        """Exact offset (seconds) from this type's own epoch."""
        return self._offset

    def __add__(self, other):
        if isinstance(other, Duration):
            return type(self)(self._offset + other.seconds)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return type(self)(self._offset - other.seconds)
        if type(other) is type(self):
            return Duration(self._offset - other._offset)
        return NotImplemented

    def __eq__(self, other):
        return type(other) is type(self) and self._offset == other._offset

    def __lt__(self, other):
        if type(other) is not type(self):
            return NotImplemented
        return self._offset < other._offset

    def __le__(self, other):
        if type(other) is not type(self):
            return NotImplemented
        return self._offset <= other._offset

    def __hash__(self):
        return hash((type(self).__name__, self._offset))

    def __repr__(self):
        return f"{type(self).__name__}({self._offset})"


class Instant(_Point):
    """A point on the abstract timeline (the canonical time-point type)."""

    __slots__ = ()


class WallClock(_Point):
    """A wall-clock reading; not a timeline point until converted."""

    __slots__ = ()


class GapError(ValueError):
    """Raised when a wall-clock reading falls into a conversion-table gap."""


class OffsetTable:
    """Synthetic offset conversion table between wall clock and timeline.

    The table is a sorted list of ``(wall_start, offset)`` entries.  Entry
    ``i`` applies to wall readings in ``[wall_start_i, wall_start_{i+1})``
    and maps ``wall -> Instant(wall + offset)``.

    * An *increase* of the offset opens a gap in instant space (skipped
      timeline points); converting such an instant raises :class:`GapError`.
    * A *decrease* of the offset would make the mapping ambiguous and is
      rejected at construction time.
    """

    __slots__ = ("_entries",)

    def __init__(self, entries: Iterable[Tuple[WallClock, Duration]]):
        rows = [(w.offset, o.seconds) for w, o in entries]
        if not rows:
            raise ValueError("offset table needs at least one entry")
        rows.sort(key=lambda row: row[0])
        for (w0, o0), (w1, o1) in zip(rows, rows[1:]):
            if w1 == w0:
                raise ValueError("duplicate wall_start in offset table")
            if o1 < o0:
                raise ValueError(
                    "offset decrease would make wall->instant mapping ambiguous"
                )
        self._entries = tuple(rows)

    @classmethod
    def synthetic(cls, base_offset=0, steps: Iterable[Tuple[int, int]] = ()) -> "OffsetTable":
        """Build a synthetic table.

        ``base_offset`` is the offset (seconds) valid from wall time 0;
        ``steps`` is an iterable of ``(wall_start, new_offset)`` changes.
        """
        entries = [(WallClock(0), Duration(base_offset))]
        entries += [(WallClock(w), Duration(o)) for w, o in steps]
        return cls(entries)

    @property
    def entries(self) -> Tuple[Tuple[Fraction, Fraction], ...]:
        return self._entries

    def _row_for_wall(self, wall: Fraction) -> int:
        starts = [w for w, _ in self._entries]
        idx = bisect_right(starts, wall) - 1
        if idx < 0:
            raise GapError(f"wall reading {wall} predates the table")
        return idx

    def to_instant(self, wall: WallClock) -> Instant:
        if not isinstance(wall, WallClock):
            raise TypeError("to_instant expects a WallClock")
        idx = self._row_for_wall(wall.offset)
        _, o0 = self._entries[idx]
        return Instant(wall.offset + o0)

    def to_wall(self, instant: Instant) -> WallClock:
        if not isinstance(instant, Instant):
            raise TypeError("to_wall expects an Instant")
        t = instant.offset
        # Candidate rows are those whose instant range contains t.
        best = None
        for i, (w0, o0) in enumerate(self._entries):
            lo = w0 + o0
            if i + 1 < len(self._entries):
                w1, _ = self._entries[i + 1]
                hi = w1 + o0  # exclusive; gap belongs to no row
            else:
                hi = None
            if t >= lo and (hi is None or t < hi):
                best = t - o0
                break
        if best is None:
            raise GapError(f"instant {t} is not covered by the table")
        return WallClock(best)

    def __repr__(self):
        return f"OffsetTable({self._entries!r})"
