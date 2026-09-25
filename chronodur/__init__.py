"""chronodur: exact duration expressions with calendar and fixed semantics.

Three kinds of time are kept strictly separate:

- :class:`Duration` -- an exact length of time (calendar months, calendar
  days, fixed rational seconds).
- :class:`Instant` -- an absolute point in time (exact seconds since epoch).
- :class:`WallTime` -- a local wall-clock reading in a SyntheticZone.
"""
from .cal import add_months, days_in_month
from .errors import (BudgetExceeded, ChronodurError, DurationOverflow,
                     ParseError, WallTimeError)
from .model import Duration, format_duration
from .parser import Budget, compile_program, parse_duration
from .program import Program
from .zone import (Instant, SyntheticZone, Transition, WallTime,
                   instant_to_wall, wall_to_instant)

__all__ = [
    "add_months", "days_in_month",
    "BudgetExceeded", "ChronodurError", "DurationOverflow", "ParseError",
    "WallTimeError",
    "Duration", "format_duration",
    "Budget", "compile_program", "parse_duration",
    "Program",
    "Instant", "SyntheticZone", "Transition", "WallTime",
    "instant_to_wall", "wall_to_instant",
]

__version__ = "0.1.0"
