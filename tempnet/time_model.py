"""Exact time model.

All constraint arithmetic happens on integer "ticks".  Floating point never
participates in comparisons, so no epsilon tolerance can hide a contradiction
(e.g. B-A in [3,5] together with A-B >= 0 is exactly infeasible).

Three distinct concepts are represented by three types:

* Duration   -- a signed length of time, measured in ticks.
* TimePoint  -- a network event's abstract event-time (relative to an unnamed
                event-time origin); only differences between TimePoints carry
                absolute meaning until the network is anchored.
* WallClock  -- a point on a chosen wall clock, i.e. a TimePoint rendered with
                a synthetic offset/conversion table (an "epoch" origin plus
                named units).

Conversion between units is always integer-valued: the synthetic offset table
(``UnitTable``) stores integer tick sizes, and ``in_ticks`` requires divisors
to divide exactly.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Dict, Mapping

__all__ = [
    "UnitTable",
    "MICROSECOND_TABLE",
    "Duration",
    "TimePoint",
    "WallClock",
]


class UnitTable:
    """Synthetic offset/conversion table: named units -> integer tick sizes.

    The table is a small ordered hierarchy.  ``base`` is the name of the base
    unit (one of it is one tick).  Every other entry maps a unit name to
    ``(parent_name, integer_multiplier)``; a unit therefore contains an exact
    integer number of ticks.  Constructing a unit of non-integer tick size is
    rejected, so conversion back to ticks never silently rounds.
    """

    __slots__ = ("base", "_factors", "_parents")

    def __init__(self, base: str, units: Mapping[str, tuple[str, int]] = ()):
        self.base = base
        self._parents: Dict[str, str] = {}
        factors: Dict[str, int] = {base: 1}
        # Resolve chains in insertion order, which must be parent-first.
        for name, (parent, mult) in units.items():
            if name == base:
                raise ValueError(f"unit {base!r} is the base unit")
            if not isinstance(mult, int) or mult <= 0:
                raise ValueError(f"unit {name!r} needs a positive integer multiplier")
            if parent not in factors:
                raise ValueError(
                    f"parent unit {parent!r} of {name!r} must be declared first"
                )
            self._parents[name] = parent
            factors[name] = factors[parent] * mult
        self._factors = factors

    def ticks(self, value: int, unit: str) -> int:
        """Exactly ``value`` units expressed in ticks (integer value only)."""
        if not isinstance(value, int):
            raise TypeError("exact time model needs integer quantities, not float")
        try:
            return value * self._factors[unit]
        except KeyError:
            raise KeyError(f"unknown unit {unit!r}") from None

    def in_unit(self, ticks: int, unit: str) -> int:
        """Convert ticks into ``unit``; raises unless it divides exactly."""
        size = self._factors[unit]
        if ticks % size:
            raise ValueError(
                f"{ticks} ticks is not an exact whole number of {unit!r}"
            )
        return ticks // size

    def tick_size(self, unit: str) -> int:
        return self._factors[unit]

    def __contains__(self, unit: object) -> bool:
        return unit in self._factors

    def units(self):
        return tuple(self._factors)


# Synthetic table: the tick is one microsecond.  Everything above it is an
# exact integer multiple; "day" is the synthetic calendar offset (24*3600 s).
MICROSECOND_TABLE = UnitTable(
    "microsecond",
    {
        "millisecond": ("microsecond", 1_000),
        "second": ("millisecond", 1_000),
        "minute": ("second", 60),
        "hour": ("minute", 60),
        "day": ("hour", 24),
    },
)


@dataclass(frozen=True)
class Duration:
    """A signed, exact duration in integer ticks."""

    ticks: int

    def __post_init__(self) -> None:
        if not isinstance(self.ticks, int):
            raise TypeError("Duration ticks must be int (floats are not accepted)")

    def in_unit(self, table: UnitTable, unit: str) -> int:
        return table.in_unit(self.ticks, unit)

    def to_wall(self, origin: "WallClock") -> "WallClock":
        """Add this duration to a wall clock instant."""
        if not isinstance(origin, WallClock):
            raise TypeError("Duration.to_wall expects a WallClock origin")
        return WallClock(origin.ticks + self.ticks, origin.table, origin.epoch_label)

    def format(self, table: UnitTable) -> str:
        """Render as e.g. ``PnDT...``-style compact exact string."""
        sign = "-" if self.ticks < 0 else ""
        t = abs(self.ticks)
        d, rem = divmod(t, table.tick_size("day")) if "day" in table else (0, t)
        parts = []
        for name in ("hour", "minute", "second", "millisecond", "microsecond"):
            if name not in table:
                continue
            size = table.tick_size(name)
            val, rem = divmod(rem, size)
            if val or parts or name in ("second", "microsecond"):
                parts.append(f"{val}{name[0] if name != 'millisecond' else 'ms'}")
        body = f"{d}d" if d else ""
        body += ("T" + "".join(parts)) if parts else ""
        if not body:
            body = "0"
        return sign + "P" + body

    def __add__(self, other: "Duration") -> "Duration":
        if isinstance(other, Duration):
            return Duration(self.ticks + other.ticks)
        return NotImplemented

    def __sub__(self, other: "Duration") -> "Duration":
        if isinstance(other, Duration):
            return Duration(self.ticks - other.ticks)
        return NotImplemented

    def __neg__(self) -> "Duration":
        return Duration(-self.ticks)

    def __eq__(self, other: object) -> bool:
        return isinstance(other, Duration) and other.ticks == self.ticks

    def __lt__(self, other: "Duration") -> bool:
        if not isinstance(other, Duration):
            return NotImplemented
        return self.ticks < other.ticks

    def __le__(self, other: "Duration") -> bool:
        if not isinstance(other, Duration):
            return NotImplemented
        return self.ticks <= other.ticks

    def __hash__(self) -> int:
        return hash((Duration, self.ticks))

    def __repr__(self) -> str:
        return f"Duration(ticks={self.ticks})"


@dataclass(frozen=True)
class TimePoint:
    """An event time as an integer tick coordinate on the unnamed event axis.

    The axis origin carries no physical meaning until the network is anchored;
    uniformly translating every point leaves all difference constraints valid.
    """

    ticks: int

    def __post_init__(self) -> None:
        if not isinstance(self.ticks, int):
            raise TypeError("TimePoint ticks must be int (floats are not accepted)")

    def __sub__(self, other: "TimePoint") -> Duration:
        if isinstance(other, TimePoint):
            return Duration(self.ticks - other.ticks)
        return NotImplemented

    def __add__(self, other: Duration) -> "TimePoint":
        if isinstance(other, Duration):
            return TimePoint(self.ticks + other.ticks)
        return NotImplemented

    def __eq__(self, other: object) -> bool:
        return isinstance(other, TimePoint) and other.ticks == self.ticks

    def __lt__(self, other: "TimePoint") -> bool:
        if not isinstance(other, TimePoint):
            return NotImplemented
        return self.ticks < other.ticks

    def __le__(self, other: "TimePoint") -> bool:
        if not isinstance(other, TimePoint):
            return NotImplemented
        return self.ticks <= other.ticks

    def __hash__(self) -> int:
        return hash((TimePoint, self.ticks))

    def to_wall(self, table: UnitTable, epoch_ticks: int = 0,
                epoch_label: str = "epoch") -> "WallClock":
        """Bind this abstract point to a wall clock with a synthetic epoch."""
        return WallClock(self.ticks + epoch_ticks, table, epoch_label)

    def __repr__(self) -> str:
        return f"TimePoint(ticks={self.ticks})"


@dataclass(frozen=True)
class WallClock:
    """An instant on a wall clock defined by a :class:`UnitTable` and epoch."""

    ticks: int
    table: UnitTable
    epoch_label: str = "epoch"

    def __post_init__(self) -> None:
        if not isinstance(self.ticks, int):
            raise TypeError("WallClock ticks must be int (floats are not accepted)")
        if not isinstance(self.table, UnitTable):
            raise TypeError("WallClock needs a UnitTable")

    def __sub__(self, other: "WallClock") -> Duration:
        if isinstance(other, WallClock):
            if other.table is not self.table:
                raise ValueError("cannot subtract instants on different clocks")
            return Duration(self.ticks - other.ticks)
        return NotImplemented

    def __add__(self, other: Duration) -> "WallClock":
        if isinstance(other, Duration):
            return WallClock(self.ticks + other.ticks, self.table, self.epoch_label)
        return NotImplemented

    def __eq__(self, other: object) -> bool:
        return (
            isinstance(other, WallClock)
            and other.ticks == self.ticks
            and other.table is self.table
        )

    def __lt__(self, other: "WallClock") -> bool:
        if not isinstance(other, WallClock) or other.table is not self.table:
            return NotImplemented
        return self.ticks < other.ticks

    def __le__(self, other: "WallClock") -> bool:
        if not isinstance(other, WallClock) or other.table is not self.table:
            return NotImplemented
        return self.ticks <= other.ticks

    def __hash__(self) -> int:
        return hash((WallClock, self.ticks, id(self.table)))

    def to_point(self, epoch_ticks: int = 0) -> TimePoint:
        """Inverse of ``TimePoint.to_wall``: strip the synthetic epoch offset."""
        return TimePoint(self.ticks - epoch_ticks)

    def format(self) -> str:
        """ISO-8601-like exact rendering using only whole table units."""
        table = self.table
        sign = "-" if self.ticks < 0 else ""
        t = abs(self.ticks)
        d, rem = divmod(t, table.tick_size("day")) if "day" in table else (0, t)
        vals = {}
        for name in ("hour", "minute", "second"):
            if name in table:
                vals[name], rem = divmod(rem, table.tick_size(name))
        micros = table.in_unit(rem, table.base) if table.base == "microsecond" else rem
        date = f"{d:04d}" if "day" in table else "0000"
        clock = f"{vals.get('hour', 0):02d}:{vals.get('minute', 0):02d}:{vals.get('second', 0):02d}"
        if micros:
            clock += f".{micros:06d}"
        return f"{sign}{date} {clock} ({self.epoch_label})"

    def __repr__(self) -> str:
        return f"WallClock(ticks={self.ticks}, epoch={self.epoch_label!r})"
