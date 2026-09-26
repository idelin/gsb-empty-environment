"""Synthetic offset transition table used by the demo and the tests.

Zone "Testland" (version ``testland-2026a``), offsets in seconds east of UTC:

- base offset +01:00
- 2026-03-29 01:00 UTC: +01:00 -> +02:00  (1h gap: wall 02:00-03:00 skipped)
- 2026-10-25 01:00 UTC: +02:00 -> +01:00  (1h fold: wall 02:00-03:00 repeated)
- 2027-01-15 00:00 UTC: +01:00 -> +01:30  (30m gap: wall 01:00-01:30 skipped)
- 2027-06-15 00:00 UTC: +01:30 -> +01:00  (30m fold: wall 01:00-01:30 repeated)
"""

from datetime import datetime, timezone

from recurrence import OffsetTable, Transition

TABLE_VERSION = "testland-2026a"


def _utc(year, month, day, hour=0):
    return int(datetime(year, month, day, hour, tzinfo=timezone.utc).timestamp())


def make_test_table(version: str = TABLE_VERSION) -> OffsetTable:
    return OffsetTable(
        version=version,
        base_offset=3600,
        transitions=[
            Transition(_utc(2026, 3, 29, 1), 3600, 7200),
            Transition(_utc(2026, 10, 25, 1), 7200, 3600),
            Transition(_utc(2027, 1, 15, 0), 3600, 5400),
            Transition(_utc(2027, 6, 15, 0), 5400, 3600),
        ],
    )
