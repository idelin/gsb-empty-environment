"""Synthetic clock model.

Three distinct notions are kept apart on purpose:

* :class:`WallReading` -- the raw, possibly faulty hardware counter value.
  It is meaningless until it passes through the node's calibration table.
* :class:`Instant` -- a point on the synthetic (calibrated) global timeline,
  expressed in whole microseconds.
* :class:`Duration` -- a signed distance between two instants, expressed in
  whole microseconds.

A "wall clock time" produced by the physical environment is always handed to
the HLC as an :class:`Instant` obtained by mapping a :class:`WallReading`
through a node-local :class:`OffsetTable`.  The HLC itself never reads a raw
counter, so a backward jump of the hardware clock is just another offset
change and cannot silently corrupt ordering.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Iterable, List


@dataclass(frozen=True, order=True)
class Duration:
    """Signed length of time, in microseconds."""

    micros: int

    # -- constructors -----------------------------------------------------
    @classmethod
    def micros_of(cls, value: int) -> "Duration":
        return cls(int(value))

    @classmethod
    def millis(cls, value: int) -> "Duration":
        return cls(int(value) * 1_000)

    @classmethod
    def seconds(cls, value: int) -> "Duration":
        return cls(int(value) * 1_000_000)

    # -- arithmetic -------------------------------------------------------
    def __add__(self, other: "Duration") -> "Duration":
        if not isinstance(other, Duration):
            return NotImplemented
        return Duration(self.micros + other.micros)

    def __sub__(self, other: "Duration") -> "Duration":
        if not isinstance(other, Duration):
            return NotImplemented
        return Duration(self.micros - other.micros)

    def __neg__(self) -> "Duration":
        return Duration(-self.micros)

    def is_negative(self) -> bool:
        return self.micros < 0

    def __abs__(self) -> "Duration":
        return Duration(-self.micros if self.micros < 0 else self.micros)


@dataclass(frozen=True, order=True)
class Instant:
    """A point on the synthetic calibrated timeline, in microseconds."""

    micros: int

    @classmethod
    def from_micros(cls, value: int) -> "Instant":
        return cls(int(value))

    @classmethod
    def from_millis(cls, value: int) -> "Instant":
        return cls(int(value) * 1_000)

    def add(self, delta: Duration) -> "Instant":
        if not isinstance(delta, Duration):
            raise TypeError("Instant.add expects a Duration")
        return Instant(self.micros + delta.micros)

    def distance_to(self, other: "Instant") -> Duration:
        if not isinstance(other, Instant):
            raise TypeError("Instant.distance_to expects an Instant")
        return Duration(other.micros - self.micros)

    def is_after(self, other: "Instant") -> bool:
        return self.micros > other.micros


@dataclass(frozen=True, order=True)
class WallReading:
    """Raw physical counter value, in the same unit (microseconds).

    Two different readings are not directly comparable as "time": the same
    raw value may denote different instants before and after an NTP step.
    """

    raw: int

    def plus_raw(self, raw_delta: int) -> "WallReading":
        return WallReading(self.raw + int(raw_delta))


@dataclass(frozen=True)
class OffsetSegment:
    """``instant = raw + offset`` while ``start_raw <= raw < end_raw``."""

    start_raw: int
    end_raw: int
    offset_micros: int

    def contains(self, raw: int) -> bool:
        return self.start_raw <= raw < self.end_raw


@dataclass(frozen=True)
class OffsetTable:
    """Piecewise-constant calibration table for one node.

    The table is the *synthetic offset conversion table* used to turn raw
    hardware readings into calibrated instants.  Segments are sorted,
    non-overlapping and half-open, so every reading maps to exactly one
    instant: time is represented precisely with no float arithmetic.
    """

    segments: tuple[OffsetSegment, ...] = field(default_factory=tuple)
    default_offset_micros: int = 0

    @classmethod
    def create(
        cls,
        segments: Iterable[tuple[int, int, int]] = (),
        default_offset_micros: int = 0,
    ) -> "OffsetTable":
        segs = sorted(
            (OffsetSegment(int(s), int(e), int(o)) for s, e, o in segments),
            key=lambda seg: seg.start_raw,
        )
        for prev, nxt in zip(segs, segs[1:]):
            if prev.end_raw > nxt.start_raw:
                raise ValueError("offset segments overlap")
        return cls(tuple(segs), int(default_offset_micros))

    def offset_for(self, raw: int) -> int:
        for seg in self.segments:
            if seg.contains(raw):
                return seg.offset_micros
            if raw < seg.start_raw:
                break
        return self.default_offset_micros

    def convert(self, reading: WallReading) -> Instant:
        return Instant(reading.raw + self.offset_for(reading.raw))


class SyntheticClock:
    """Controllable physical clock for one node.

    The raw counter only moves forward via :meth:`advance_raw`; observed wall
    clock jumps (NTP correction, backward step, far-future leaps) are modelled
    exclusively by replacing the offset table, which is exactly the failure
    mode a real node faces when its wall clock is steered.
    """

    def __init__(self, start: WallReading, table: OffsetTable | None = None) -> None:
        self._reading = start
        self._table = table if table is not None else OffsetTable.create()

    @property
    def reading(self) -> WallReading:
        return self._reading

    @property
    def table(self) -> OffsetTable:
        return self._table

    def advance_raw(self, raw_delta: int) -> WallReading:
        self._reading = self._reading.plus_raw(raw_delta)
        return self._reading

    def install_table(self, table: OffsetTable) -> None:
        self._table = table

    def now(self) -> Instant:
        return self._table.convert(self._reading)
