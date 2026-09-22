"""Query demos for the two timelines.

Timeline 1 -- "10时发生了什么" (valid-time axis):
    fix the observation time, sweep the business time.

Timeline 2 -- "当时我们知道什么" (transaction-time axis):
    fix the business time, sweep the observation time.

Run:  python3.11 demo.py
"""

from __future__ import annotations

from bitemporal import BitemporalIndex
from fixtures import EXPECTED_ALPHA_AT_10, OBSERVATIONS, build_index

NOW = 60  # "today": every record in the fixtures is visible at tx 60


def show(result) -> str:
    if result.record is None:
        return "(no record)"
    if result.retracted:
        return "(retracted: known to have no value)"
    return repr(result.value)


def timeline_what_happened(idx: BitemporalIndex) -> None:
    print("== Timeline 1: '10时发生了什么' -- sweep valid time, as_of fixed "
          f"at {NOW} ==")
    for obj in ("alpha", "beta", "gamma", "delta"):
        for t in (5, 9, 10, 15, 25, 1000):
            r = idx.query(obj, valid_time=t, as_of=NOW)
            print(f"  {obj:5s} valid={t:5} as_of={NOW}  -> {show(r)}")
        print()


def timeline_what_we_knew(idx: BitemporalIndex) -> None:
    print("== Timeline 2: '当时我们知道什么' -- valid time fixed at 10, "
          "sweep observation time ==")
    for as_of in OBSERVATIONS:
        r = idx.query("alpha", valid_time=10, as_of=as_of)
        expected = EXPECTED_ALPHA_AT_10[as_of]
        mark = "OK " if r.value == expected else "MISMATCH"
        print(f"  [{mark}] alpha valid=10 as_of={as_of:3} -> {show(r)}")
    print()
    print("  Backfill visibility (delta, valid=10):")
    for as_of in (44, 45):
        r = idx.query("delta", valid_time=10, as_of=as_of)
        print(f"    as_of={as_of:3} -> {show(r)}")


def main() -> None:
    idx = build_index()
    timeline_what_happened(idx)
    timeline_what_we_knew(idx)


if __name__ == "__main__":
    main()
