"""Synthetic offset-transition tables used as test/demo fixtures.

The tables are deliberately fabricated (no host zone database involved) but
mimic a DST zone: ``+08:00`` standard time and ``+09:00`` daylight time.
Clocks spring forward at 02:00 standard on the second Sunday of March and
fall back at 02:00 daylight on the first Sunday of November.  Transitions
are expressed at their UTC instants, so the local wall effects are:

* spring forward -- wall 02:00..03:00 does not exist (a one-hour gap);
* fall back      -- wall 01:00..02:00 happens twice (a one-hour overlap).
"""

from __future__ import annotations

import datetime as _dt

from .offsets import OffsetTable, Transition
from .time_model import Instant

H = 3600 * 1_000_000  # one hour in microseconds
STD = 8 * H
DST = 9 * H


def _second_sunday(year: int) -> _dt.date:
    first = _dt.date(year, 3, 1)
    shift = (6 - first.weekday()) % 7  # days until first Sunday
    return first + _dt.timedelta(days=shift + 7)


def _first_sunday(year: int) -> _dt.date:
    first = _dt.date(year, 11, 1)
    shift = (6 - first.weekday()) % 7
    return first + _dt.timedelta(days=shift)


def _utc_instant_local(date: _dt.date, local_hour: int, offset_us: int) -> Instant:
    """Instant at which ``local_hour`` on ``date`` is reached under ``offset``."""
    local_epoch = (date - _dt.date(1970, 1, 1)).days * 86_400 * 1_000_000
    local_epoch += local_hour * H
    return Instant(local_epoch - offset_us)


def make_table(
    *,
    start_year: int = 2022,
    end_year: int = 2030,
    name: str = "synthetic-dst",
    initial: str = "std",
) -> OffsetTable:
    """Build a +08/+09 DST table covering the requested year range."""
    transitions: list[Transition] = []
    jan1 = _dt.date(start_year, 1, 1)
    jan1_epoch = (jan1 - _dt.date(1970, 1, 1)).days * 86_400 * 1_000_000
    if initial == "std":
        transitions.append(Transition(Instant(jan1_epoch - STD), STD, "S"))
    elif initial == "dst":
        transitions.append(Transition(Instant(jan1_epoch - DST), DST, "D"))
    else:
        raise ValueError("initial must be 'std' or 'dst'")

    for year in range(start_year, end_year + 1):
        spring = _second_sunday(year)
        transitions.append(
            Transition(_utc_instant_local(spring, 2, STD), DST, "D")
        )
        fall = _first_sunday(year)
        transitions.append(
            Transition(_utc_instant_local(fall, 2, DST), STD, "S")
        )
    return OffsetTable(transitions, name=name)


def make_modified_table(*, start_year: int = 2022, end_year: int = 2030) -> OffsetTable:
    """A different-content table to exercise cursor/table-version binding."""
    return make_table(
        start_year=start_year,
        end_year=end_year,
        name="synthetic-dst-v2",
        initial="dst",
    )
