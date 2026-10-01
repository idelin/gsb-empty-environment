"""Shared helpers: exact UTC instants and synthetic offset tables."""

from datetime import date
from fractions import Fraction

from calpart.exact import EPOCH_DATE, SECONDS_PER_DAY
from calpart.offsets import OffsetTable, Transition


def utc(y, mo, d, h=0, mi=0, s=0) -> Fraction:
    """Exact UTC instant (seconds since epoch) for a civil date/time."""
    days = (date(y, mo, d) - EPOCH_DATE).days
    return Fraction(days * SECONDS_PER_DAY + h * 3600 + mi * 60 + s)


# Pure UTC, no transitions.
UTC = OffsetTable(0)

# "Fall back": +02:00 -> +01:00 at 2023-10-29T01:00:00Z.
# Local date 2023-10-29 therefore lasts 25 hours.
FALLBACK = OffsetTable(7200, [Transition(utc(2023, 10, 29, 1), 3600)])

# "Spring forward": +01:00 -> +02:00 at 2023-03-26T01:00:00Z.
# Local 02:00..03:00 never happens (a wall-clock gap).
SPRING = OffsetTable(3600, [Transition(utc(2023, 3, 26, 1), 7200)])

# Samoa-style skipped date: -10:00 -> +14:00 at 2011-12-30T10:00:00Z.
# Local date 2011-12-30 has no preimage at all.
SKIP_DAY = OffsetTable(-36000, [Transition(utc(2011, 12, 30, 10), 14 * 3600)])

# Offset jumps backwards by 30h at 2020-06-15T00:00:00Z, so local dates
# 2020-06-13/14 each occur twice (two disjoint UTC preimages).
REPEAT_DAY = OffsetTable(0, [Transition(utc(2020, 6, 15, 0), -30 * 3600)])
