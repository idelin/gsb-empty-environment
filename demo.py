"""Runnable sample: versioned valid-time timelines end to end.

Run with:  python3.11 demo.py
"""
from datetime import datetime, timezone
from fractions import Fraction

from temporal import (
    Change,
    ChangeKind,
    Delta,
    Duration,
    OffsetTable,
    Segment,
    TimePoint,
    Timeline,
    WallClock,
    apply,
    diff,
    invert,
    merge3,
    rebase,
    scan_equal,
    summarize,
    verify_inverse,
    verify_summary,
)


def tp(n):
    return TimePoint(n)


def show(title):
    print(f"\n=== {title} " + "=" * max(0, 60 - len(title)))


def show_changes(label, changes):
    print(f"{label}: {len(changes)} minimal change segment(s)")
    for ch in changes:
        print(f"  {ch.kind.value:6s} [{ch.start.ticks}, {ch.end.ticks}) "
              f"{ch.old!r} -> {ch.new!r}")


def main():
    show("Synthetic offset table: wall clock vs time point vs duration")
    table = OffsetTable([
        (WallClock(datetime(2026, 9, 26, 0, 0, tzinfo=timezone.utc)),
         TimePoint(0), 2),                       # 2 ticks per second
        (WallClock(datetime(2026, 9, 26, 1, 0, tzinfo=timezone.utc)),
         TimePoint(7200), Fraction(3, 2)),       # then 1.5 ticks per second
    ])
    reading = WallClock(datetime(2026, 9, 26, 1, 0, 2, tzinfo=timezone.utc))
    point = table.to_time_point(reading)
    print(f"wall clock {reading.at.isoformat()}  ->  time point {point.ticks} ticks")
    print(f"roundtrip back to wall clock: {table.to_wall_clock(point).at.isoformat()}")
    print(f"exact duration t=1/3 -> t=1: {(tp(1) - tp(Fraction(1, 3))).ticks} ticks")
    print("types are distinct: WallClock, TimePoint, Duration are not interchangeable")

    show("Base timeline and baseline summary")
    base = Timeline([
        Segment(tp(0), tp(10), "idle"),
        Segment(tp(10), tp(20), "running"),
        Segment(tp(20), tp(30), "cooldown"),
    ])
    baseline = summarize(base)
    print(f"base: {base}")
    print(f"baseline summary: nodes={baseline.nodes} span="
          f"[{baseline.start.ticks}, {baseline.end.ticks}) digest={baseline.digest[:16]}...")

    show("Minimal diff: insert / delete / update, no zero-length output")
    edited = Timeline([
        Segment(tp(0), tp(4), "idle"),
        Segment(tp(4), tp(10), "starting"),      # UPDATE on [4,10)
        Segment(tp(10), tp(14), "running"),      # unchanged
        Segment(tp(14), tp(16), "paused"),       # UPDATE on [14,16)
        Segment(tp(16), tp(20), "running"),      # unchanged
        # [20,30) cooldown removed               # DELETE
        Segment(tp(30), tp(36), "off"),          # INSERT into the gap
    ])
    delta = diff(base, edited)
    show_changes("diff(base, edited)", delta)

    show("Reversible application + baseline summary verification")
    forward = apply(base, delta)
    print(f"apply(base, delta) == edited: {scan_equal(forward.timeline, edited)}")
    restored = apply(forward.timeline, invert(delta))
    print(f"apply(result, inverse) restores base: {scan_equal(restored.timeline, base)}")
    print(f"verify_inverse(full endpoint scan): {verify_inverse(base, delta)}")
    print(f"restored matches baseline summary: {verify_summary(restored.timeline, baseline)}")

    show("Structural sharing: old snapshot keeps its nodes")
    small = apply(base, [Change(ChangeKind.UPDATE, tp(10), tp(20),
                                "running", "turbo")])
    print(f"single-segment edit -> copied nodes: {small.copied_nodes}, "
          f"shared nodes: {small.shared_nodes} (of {len(small.timeline)})")
    print(f"shared node identity: {small.timeline.segments[0] is base.segments[0]}")
    print(f"base snapshot still intact: {base}")

    show("Three-way merge of two independent branch deltas")
    branch_a = diff(base, Timeline([
        Segment(tp(0), tp(10), "idle"),
        Segment(tp(10), tp(18), "turbo"),        # A: [10,18) -> turbo
        Segment(tp(18), tp(20), "running"),
        Segment(tp(20), tp(30), "cooldown"),
    ]))
    branch_b = diff(base, Timeline([
        Segment(tp(0), tp(10), "idle"),
        Segment(tp(12), tp(20), "eco"),          # B: [12,20) -> eco
        Segment(tp(20), tp(30), "maintenance"),  # B: [20,30) -> maintenance
    ]))
    show_changes("branch A", branch_a)
    show_changes("branch B", branch_b)
    merged = merge3(base, branch_a, branch_b)
    print(f"merged ok: {merged.ok}")
    for c in merged.conflicts:
        print(f"  minimal conflict [{c.start.ticks}, {c.end.ticks}): "
              f"base={c.base_value!r} A={c.a_value!r} B={c.b_value!r}")
    print(f"merged timeline: {merged.timeline}")
    print(f"(conflict region keeps the base value; non-conflicting parts of "
          f"both branches applied; copied={merged.copied_nodes} shared={merged.shared_nodes})")

    show("Rebase of branch B onto branch A")
    rebased = rebase(base, branch_a, branch_b)
    print(f"rebase ok: {rebased.ok}, conflicts: {len(rebased.conflicts)}")
    show_changes("rebased delta (onto A's result)", rebased.changes)
    print("(conflicting parts of B were dropped, so the rebased delta also "
          "reverts the conflict region back to the base value)")

    show("Identified corrections and retraction")
    delta_log = Delta()
    delta_log.add(Change(ChangeKind.UPDATE, tp(0), tp(10), "idle", "warmup",
                         identity="fix-1"))
    print(f"after fix-1:            {apply(base, delta_log.changes()).timeline}")
    delta_log.correct(Change(ChangeKind.UPDATE, tp(0), tp(10), "idle", "preheat",
                             identity="fix-1"))
    print(f"after correcting fix-1: {apply(base, delta_log.changes()).timeline}")
    delta_log.retract("fix-1")
    restored_base = apply(base, delta_log.changes()).timeline
    print(f"after retracting fix-1: {restored_base}")
    print(f"retraction restored the base exactly: {scan_equal(restored_base, base)}")
    print("(retraction is by identity, not by matching the current value)")


if __name__ == "__main__":
    main()
