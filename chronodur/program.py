"""Compiled duration programs: ordered, composable operation sequences."""
from __future__ import annotations

import datetime as dt
from dataclasses import dataclass

from .cal import add_months
from .zone import Instant, WallTime

ADD_MONTHS = "mo"
ADD_CAL_DAYS = "cd"
ADD_FIXED = "fx"


@dataclass(frozen=True)
class Program:
    """An ordered sequence of calendar/fixed operations.

    Order is significant: calendar months/days and fixed seconds do not
    commute, so a Program never reorders its operations. Programs compose
    with ``then`` and apply to a relative base (a WallTime).
    """

    ops: tuple = ()

    def then(self, other: "Program") -> "Program":
        return Program(self.ops + other.ops)

    def apply(self, wall: WallTime) -> WallTime:
        w = wall
        for kind, amount in self.ops:
            if kind == ADD_MONTHS:
                w = WallTime(dt.datetime.combine(
                    add_months(w.dt.date(), amount), w.dt.time()), w.zone, w.sub)
            elif kind == ADD_CAL_DAYS:
                w = WallTime(dt.datetime.combine(
                    w.dt.date() + dt.timedelta(days=amount), w.dt.time()),
                    w.zone, w.sub)
            elif kind == ADD_FIXED:
                w = Instant(w.to_instant().seconds + amount).to_wall(w.zone)
            else:  # pragma: no cover - defensive
                raise AssertionError(f"unknown op {kind!r}")
        return w
