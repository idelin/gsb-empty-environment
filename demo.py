"""Runnable sample: retroactive corrections on a bitemporal salary ledger.

Run with:  python3.11 demo.py
"""

from datetime import datetime

from bitemporal import (BitemporalStore, Duration, Instant, Timeline,
                        TimeModelError)
from bitemporal.checks import check_store
from bitemporal.index import QueryStats


def show(store, key, when, as_of, label):
    print(f"  {label:<58} -> {store.value_at(key, when, as_of)}")


def main() -> None:
    print("== 1. Exact time model: wall clock vs instant vs duration ==")
    timeline = Timeline()
    timeline.register_offset("CST", Duration.of(hours=8))  # UTC+8
    print("  conversion table:", {k: str(v) for k, v in timeline.conversion_table().items()})
    wall = timeline.wall("CST", datetime(2024, 6, 1, 12, 0))
    instant = timeline.instant(wall)
    print(f"  wall clock {wall}  ==  instant {instant}")
    print(f"  instant + Duration.of(days=30) = {instant + Duration.of(days=30)}")
    try:
        wall + Duration.of(days=1)
    except TimeModelError as exc:
        print(f"  WallClock arithmetic rejected: {exc}")

    def cst(y, m, d):
        return timeline.at("CST", datetime(y, m, d))

    JAN, FEB, MAR, APR, MAY, JUN, SEP = (cst(2024, 1, 1), cst(2024, 2, 1),
                                         cst(2024, 3, 1), cst(2024, 4, 1),
                                         cst(2024, 5, 1), cst(2024, 6, 1),
                                         cst(2024, 9, 1))
    store = BitemporalStore(timeline)

    print("\n== 2. Retroactive correction splits an open-ended rectangle ==")
    with store.transaction() as tx:            # version 1
        tx.upsert("alice", JAN, None, value=100, priority=0)
    with store.transaction() as tx:            # version 2: correction Mar..Sep
        tx.upsert("alice", MAR, SEP, value=120, priority=10)
    show(store, "alice", JUN, 1, "alice @2024-06 as observed at version 1")
    show(store, "alice", JUN, 2, "alice @2024-06 as observed at version 2 (correction)")
    show(store, "alice", JUN, None, "alice @2024-06 at latest version")

    print("\n== 3. Retraction does not overwrite history ==")
    with store.transaction() as tx:            # version 3: retract the correction
        tx.retract(2)
    show(store, "alice", JUN, None, "alice @2024-06 at latest (correction retracted)")
    show(store, "alice", JUN, 2, "alice @2024-06 as observed at version 2 (unchanged)")

    print("\n== 4. One atomic transaction: upsert + reprioritize ==")
    with store.transaction() as tx:            # version 4
        tx.upsert("alice", APR, MAY, value=110, priority=5)
        tx.change_priority(1, 20)              # base record outranks the new one
    show(store, "alice", APR, 3, "alice @2024-04 as observed at version 3")
    show(store, "alice", APR, None, "alice @2024-04 at latest (reprioritized base wins)")

    print("\n== 5. Rectangle range query with provenance ==")
    stats = QueryStats()
    frags = store.range_query("alice", JAN, cst(2025, 1, 1), ver_from=1, stats=stats)
    for f in frags:
        v_to = "now" if f.ver_to is None else f"v{f.ver_to}"
        valid_to = "open" if f.valid_to is None else timeline.iso(f.valid_to, "CST")
        print(f"  valid [{timeline.iso(f.valid_from, 'CST')}, {valid_to}) "
              f"x versions [v{f.ver_from}, {v_to})"
              f"  value={f.value}  priority={f.priority}  source=record #{f.record_id}")
    print(f"  {stats}; history log holds {store.total_records} records "
          f"(query never scans the log)")

    print("\n== 6. Provenance explanation of a single point ==")
    expl = store.explain("alice", APR, as_of=4)
    print(f"  value at {timeline.iso(APR, 'CST')} as of v{expl.as_of}: {expl.value}")
    for c in expl.competitors:
        print(f"    record #{c.record_id} (priority {c.priority}, recorded v"
              f"{c.recorded_version}, value {c.value}): {c.status}")

    print("\n== 7. Independent per-version invariant check ==")
    report = check_store(store)
    print(f"  {report}")


if __name__ == "__main__":
    main()
