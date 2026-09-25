"""Core data model: measurements, versioned nodes, and error types.

Priority rule for nodes sharing the same coordinate (explicit and total):
the active node is the one with the highest ``priority``; ties are broken by
the highest ``version``.  A channel absent from ``Node.values`` is *unknown*
at that coordinate -- it is never treated as zero.
"""

from __future__ import annotations

from dataclasses import dataclass

from .interval import Interval


class PwlinError(Exception):
    """Base class for all pwlin errors."""


class NoDataError(PwlinError):
    """Raised when a channel has no usable data (never silently zero)."""


class OutOfDomainError(PwlinError):
    """Raised when a query falls outside the breakpoint range."""


class UnknownChannelError(PwlinError):
    """Raised when referencing a channel not declared by the table."""


@dataclass(frozen=True)
class Measurement:
    """A measured value known to lie inside [lo, hi] (error interval)."""

    lo: float
    hi: float

    def __post_init__(self) -> None:
        if self.lo > self.hi:
            raise ValueError(f"measurement with lo > hi: [{self.lo!r}, {self.hi!r}]")

    @classmethod
    def exact(cls, value: float) -> "Measurement":
        return cls(value, value)

    def interval(self) -> Interval:
        return Interval(self.lo, self.hi)


@dataclass(frozen=True)
class Node:
    """A breakpoint at coordinate ``x`` with per-channel measurements.

    ``values`` maps channel name -> Measurement.  Channels missing from the
    mapping are unknown at this coordinate and are excluded from that
    channel's curve (they are never filled with zero).
    """

    x: float
    version: int
    priority: int
    values: dict

    def __post_init__(self) -> None:
        if not self.values:
            raise ValueError("a node must carry at least one channel value")
        for channel, meas in self.values.items():
            if not isinstance(meas, Measurement):
                raise TypeError(f"channel {channel!r}: expected Measurement, got {type(meas)!r}")

    def order_key(self) -> tuple:
        """Explicit priority rule: higher priority wins, then higher version."""
        return (self.priority, self.version)
