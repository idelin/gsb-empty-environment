"""Synthetic fixtures exercising every required edge case.

Objects
-------
alpha   The canonical scenario: fact at valid time 10 recorded as A at
        tx 20, corrected to B at tx 30, corrected again to C at tx 35
        (multiple corrections of the same fact), retroactively retracted
        at tx 40, re-asserted as D at tx 50.
beta    Two adjacent [left-closed, right-open) intervals meeting at 10.
gamma   A "future version": valid interval starts at 1000; must never
        pollute queries about valid time 10.
delta   A late backfill: knowledge about valid [5, 15) only recorded at
        tx 45 -- invisible to earlier observation times.
"""

from __future__ import annotations

from bitemporal import BitemporalIndex, Kind, OPEN_ENDED

INF = OPEN_ENDED


def build_index() -> BitemporalIndex:
    idx = BitemporalIndex()

    # --- alpha: corrections, multi-correction, retraction, re-assert ---
    idx.add("alpha", 10, INF, recorded_at=20, kind=Kind.ASSERT, value="A")
    idx.add("alpha", 10, INF, recorded_at=30, kind=Kind.CORRECT, value="B")
    idx.add("alpha", 10, INF, recorded_at=35, kind=Kind.CORRECT, value="C")
    idx.add("alpha", 10, INF, recorded_at=40, kind=Kind.RETRACT)
    idx.add("alpha", 10, INF, recorded_at=50, kind=Kind.ASSERT, value="D")

    # --- beta: adjacent intervals, boundary belongs to the right one ---
    idx.add("beta", 0, 10, recorded_at=5, kind=Kind.ASSERT, value="X")
    idx.add("beta", 10, 20, recorded_at=5, kind=Kind.ASSERT, value="Y")

    # --- gamma: future version, recorded now but valid far ahead ---
    idx.add("gamma", 1000, INF, recorded_at=25, kind=Kind.ASSERT, value="FUTURE")

    # --- delta: backfilled knowledge about the past ---
    idx.add("delta", 5, 15, recorded_at=45, kind=Kind.ASSERT, value="BACKFILL")

    return idx


#: Observation times used by the demos/tests for object "alpha".
OBSERVATIONS = [15, 25, 32, 37, 45, 55]

#: Expected value of query("alpha", valid_time=10, as_of=t) -- the
#: contract every test and demo checks.
EXPECTED_ALPHA_AT_10 = {
    15: None,   # nothing recorded yet
    25: "A",    # only the original assertion is visible
    32: "B",    # first correction visible, second not yet
    37: "C",    # second correction
    45: None,   # retroactively retracted
    55: "D",    # re-asserted
}
