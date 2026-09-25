"""End-to-end demo of the bucketindex package.

Run with:  python3.11 examples/demo.py
"""

from fractions import Fraction
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from bucketindex import (
    Duration,
    EventStore,
    FineBuckets,
    Instant,
    OffsetTable,
    WallClock,
)


def main() -> None:
    print("=== 1. Exact time model: wall clock vs instant vs duration ===")
    # Synthetic offset translation table: two segments, exact rational rates.
    table = OffsetTable([(0, 0, 10), (100, 1000, 2)])
    wall = WallClock(105)
    instant = table.wall_to_instant(wall)
    print(f"wall {wall.reading} -> instant {instant.ticks} ticks "
          f"(exact {table.wall_to_ticks_exact(wall)})")
    print(f"instant {instant.ticks} -> wall {table.instant_to_wall(instant).reading}")
    frac = WallClock(Fraction(1003, 10))  # wall reading 100.3
    print(f"wall {frac.reading} -> exact {table.wall_to_ticks_exact(frac)} ticks, "
          f"floored to instant {table.wall_to_instant(frac).ticks}")
    d = table.duration_between(WallClock(90), WallClock(110))
    print(f"duration between wall 90 and 110: {d.ticks} ticks "
          f"(type: {type(d).__name__})")
    print(f"wall(10) - wall(4) is a {type(WallClock(10) - WallClock(4)).__name__}, "
          f"not a Duration")

    print("\n=== 2. Ingest events: late arrivals, correction, retraction ===")
    store = EventStore(FineBuckets(origin=0, width=10))
    store.ingest("e1", table.wall_to_instant(WallClock(5)))     # tick 50
    store.ingest("e2", table.wall_to_instant(WallClock(101)))   # tick 1002
    store.ingest("e3", Instant(-1))     # negative time: bucket [-10, 0)
    store.ingest("e4", Instant(-25))    # late arrival, out of order
    store.ingest("e5", Instant(3))
    store.correct("e2", Instant(77), version=2)  # correction moves the event
    store.ingest("e6", Instant(3))      # duplicate timestamp
    store.retract("e4")                 # retraction
    for c in store.history:
        print(f"  {c.kind:7s} {c.event_id} v{c.version} @ tick {c.ticks}")
    print(f"live events: {store.total()}")

    print("\n=== 3. Exact range counts with itemised edges ===")
    r = store.count_range(Instant(-5), Instant(100))
    print(f"count[-5, 100)   = {r.total} "
          f"(internal {r.internal} + edge items {sorted(it[0] for it in r.edge_items)})")
    r = store.count_range(Instant(-10), Instant(80))
    print(f"count[-10, 80)   = {r.total} (aligned: internal only, {r.internal})")

    print("\n=== 4. k-th event and quantiles (ties by stable id) ===")
    for k in range(store.total()):
        print(f"  k={k}: {store.kth(k)}")
    print(f"median time: {store.quantile(0.5)}")

    print("\n=== 5. Versioned business rules; old snapshots stay valid ===")
    rule_v1 = store.rules.register((0, 50))
    print(f"rule v1 {rule_v1.boundaries}: {store.business_counts(rule_v1)}")
    root_before = store.root_at()
    rule_v2 = store.rules.register((-10, 40, 80))
    print(f"rule v2 {rule_v2.boundaries}: {store.business_counts(rule_v2)}")
    print(f"rule v1 after update (snapshot): {store.business_counts(rule_v1)}")
    print(f"tree rebuilt on rule change? {store.root_at() is not root_before}")

    print("\n=== 6. Contribution lineage ===")
    print("explain[0, 100):", store.explain_range(Instant(0), Instant(100)))


if __name__ == "__main__":
    main()
