"""Synthetic fixtures covering the required scenarios.

Times are plain integers so the examples stay readable:

* acct-1  event at 10 recorded as A at tx=20, corrected to B at tx=30,
          retroactively retracted at tx=40;
* acct-2  one record corrected twice (tx=20/22/24);
* acct-3  a future version: recorded at tx=25, valid only in [100, 200);
* acct-4  two abutting intervals [0,10) and [10,20) recorded together;
* acct-5  a late backfill: event in [5,8) only recorded at tx=50.
"""

from __future__ import annotations

from .model import INF, Kind, Version


def prompt_scenario() -> list[Version]:
    """The minimal example: value at 10 recorded A@20, corrected B@30."""
    return [
        Version("acct-1", 10, INF, 20, 1, Kind.ASSERT, "A"),
        Version("acct-1", 10, INF, 30, 2, Kind.CORRECT, "B"),
    ]


def full_fixture() -> list[Version]:
    return [
        # acct-1: correction chain + retroactive retraction
        Version("acct-1", 10, INF, 20, 1, Kind.ASSERT, "A"),
        Version("acct-1", 10, INF, 30, 2, Kind.CORRECT, "B"),
        Version("acct-1", 10, INF, 40, 3, Kind.RETRACT),
        # acct-2: the same record corrected twice
        Version("acct-2", 10, INF, 20, 1, Kind.ASSERT, "A"),
        Version("acct-2", 10, INF, 22, 2, Kind.CORRECT, "A'"),
        Version("acct-2", 10, INF, 24, 3, Kind.CORRECT, "A''"),
        # acct-3: future version recorded early
        Version("acct-3", 100, 200, 25, 1, Kind.ASSERT, "future"),
        # acct-4: abutting intervals recorded at the same moment
        Version("acct-4", 0, 10, 5, 1, Kind.ASSERT, "p"),
        Version("acct-4", 10, 20, 5, 2, Kind.ASSERT, "q"),
        # acct-5: old event backfilled late
        Version("acct-5", 5, 8, 50, 1, Kind.ASSERT, "old"),
    ]
