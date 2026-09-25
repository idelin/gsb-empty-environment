"""Runnable demo of the layered event-count engine (Python 3.11, stdlib only)."""

from fractions import Fraction

from buckets import BucketSchema
from engine import EventEngine
from timemodel import Duration, OffsetTable, TimePoint, WallClock


def show(title, value):
    print(f"{title:<58} {value}")


def main():
    print("== time model: wall clock / time point / duration ==")
    table = OffsetTable([
        (Fraction(0), 0, Fraction(1000)),        # 1 tick = 1 ms from t=0s
        (Fraction(60), 100_000, Fraction(1000)), # synthetic jump at t=60s
    ])
    point = table.wall_to_point(WallClock(Fraction(61)))
    show("wall 61s -> synthetic ticks", point)
    show("ticks -> wall clock (exact)", table.point_to_wall(point))
    show("point + duration", point + Duration(250))
    show("duration between points", point + Duration(250) - point)

    print("\n== floor buckets: -1 falls in [-10, 0), never [0, 10) ==")
    schema = BucketSchema(fine_width=10,
                          business_boundaries=(-20, 0, 20, 50), version=0)
    show("leaf_bucket(-1)", f"{schema.leaf_bucket(-1)} -> {schema.bucket_span(-1)}")
    show("leaf_bucket(0)", f"{schema.leaf_bucket(0)} -> {schema.bucket_span(0)}")

    print("\n== ingest: late events, correction, retraction ==")
    engine = EventEngine(schema)
    for eid, t in [("e1", -25), ("e2", -15), ("e3", -1), ("e4", 0), ("e5", 3),
                   ("e6", 3), ("e7", 17), ("e8", 25), ("e9", 33), ("e10", 49)]:
        engine.add(eid, t)
    engine.add("late", -500)          # out-of-order arrival
    engine.correct("e8", 19)          # correction: 25 -> 19, keeps stable id
    engine.retract("e1")              # retraction
    show("live events (time, seq, id)", engine.items())

    print("\n== layered range count over [-15, 37) ==")
    total, contribs = engine.range_count(-15, 37)
    for c in contribs:
        detail = f" items={[(t, e) for t, _s, e in c.items]}" if c.items else ""
        print(f"  [{c.lo:>4}, {c.hi:>4}) {c.layer:<9} count={c.count} "
              f"src={c.source} (data v{c.data_version}, schema v{c.schema_version})"
              f"{detail}")
    show("total (sum of disjoint contributions)", total)

    print("\n== order statistics on the count index ==")
    show("3rd event (kth)", engine.kth(3))
    show("median time (quantile 0.5)", engine.quantile(0.5))
    show("p90 time (quantile 0.9)", engine.quantile(0.9))

    print("\n== bucket rule update: old snapshots stay valid ==")
    snap = engine.snapshot()
    show("snapshot before schema update", engine.range_count(-20, 30, vid=snap)[0])
    engine.update_schema(BucketSchema(fine_width=5,
                                      business_boundaries=(-20, 5, 30), version=1))
    engine.add("new", 6)
    show("same snapshot after update (unchanged)",
         engine.range_count(-20, 30, vid=snap)[0])
    total, contribs = engine.range_count(-20, 30)
    show("head with new schema v1", total)
    for c in contribs:
        print(f"  [{c.lo:>4}, {c.hi:>4}) {c.layer:<9} count={c.count} "
              f"(schema v{c.schema_version})")


if __name__ == "__main__":
    main()
