"""End-to-end demo of the priority-stacked, provenance-tracked timeline.

Run from the repository root:

    python3.11 examples/demo.py
"""

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from timestack import Duration, Instant, OffsetTable, Timeline, WallClock


def show(title, tl):
    print(f"\n== {title} ==")
    for p in tl.pieces():
        span = f"[{p.start.offset}, {p.end.offset})"
        if p.is_conflict:
            choices = ", ".join(
                f"{v!r} <- {sorted(s)}" for v, s in p.conflict.choices
            )
            print(f"  {span:12} CONFLICT: {choices}")
        else:
            print(f"  {span:12} {p.value!r:<8} sources={sorted(p.sources)}")


def main():
    tl = Timeline()

    # 1. Base layer: low priority covers [0, 10).
    tl.add("sensor-low", 0, 10, "L", priority=0)
    show("base layer: low priority [0,10)", tl)

    # 2. High priority [3,7) overrides only the middle segment.
    high = tl.add("manual-high", 3, 7, "H", priority=10)
    show("high priority [3,7) overrides the middle only", tl)

    # 3. Retracting the high-priority segment restores the underlying layer.
    tl.remove(high)
    show("high priority retracted -> base restored", tl)

    # 4. Same-priority disagreement is preserved explicitly as a conflict.
    tl.add("feed-a", 2, 6, "X", priority=5)
    tl.add("feed-b", 4, 8, "Y", priority=5)
    show("same-priority conflict kept explicit on [4,6)", tl)
    conflict, sources = tl.value_at(5)
    print(f"  value_at(5) -> conflict values={sorted(conflict.values)}, sources={sorted(sources)}")

    # 5. Adjacent equal values from different sources are NOT merged away:
    #    provenance evidence is kept per piece.
    tl2 = Timeline()
    tl2.add("meter-1", 0, 5, "ON", priority=0)
    tl2.add("meter-2", 5, 10, "ON", priority=0)
    show("adjacent same value, different sources: evidence preserved", tl2)

    # 6. Exact time: fractional boundaries, no float drift.
    from fractions import Fraction
    tl3 = Timeline()
    tl3.add("frac", Fraction(1, 3), Fraction(1, 2), "F", priority=0)
    piece = tl3.pieces()[0]
    print(f"\n== exact arithmetic ==")
    print(f"  piece duration = {piece.duration.seconds} s (exact Fraction)")
    t = Instant(0)
    for _ in range(10):
        t = t + Duration(Fraction(1, 10))
    print(f"  10 x 1/10 s accumulated exactly: {t.offset} (== 1: {t.offset == 1})")

    # 7. Wall clock vs instant vs duration, via a synthetic offset table.
    table = OffsetTable.synthetic(base_offset=2, steps=[(100, 5)])
    tl4 = Timeline(offset_table=table)
    tl4.add("wall", WallClock(10), WallClock(20), "W", priority=0)
    p = tl4.pieces()[0]
    print(f"\n== wall clock / instant / duration ==")
    print(f"  WallClock(10) -> {table.to_instant(WallClock(10))}")
    print(f"  piece on timeline: [{p.start.offset}, {p.end.offset})  (instants)")
    print(f"  back to wall: [{table.to_wall(p.start).offset}, {table.to_wall(p.end).offset})")
    print(f"  duration: {p.duration.seconds} s")


if __name__ == "__main__":
    main()
