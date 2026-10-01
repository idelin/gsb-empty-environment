"""Runnable samples for calpart (Python 3.11, standard library only)."""
from fractions import Fraction

from calpart import (
    CalendarSpec,
    DayGrid,
    Instant,
    MonthGrid,
    OffsetTable,
    SegmentPager,
    Wall,
    WeekGrid,
    WorkStats,
    common_refinement,
    format_civil,
    iter_segments,
    jump_scan,
    partition,
    verify_piecewise_conservation,
    verify_segments_cover,
)


def U(year, month, day, hour=0, minute=0, second=0, fraction=0):
    return Instant.from_civil(year, month, day, hour, minute, second, fraction).seconds


def europe_like():
    transitions = []
    for year, march_day, october_day in ((2023, 26, 29), (2024, 31, 27), (2025, 30, 26)):
        transitions.append((U(year, 3, march_day, 1), 7200))
        transitions.append((U(year, 10, october_day, 1), 3600))
    return OffsetTable(3600, transitions)


def fmt(seconds):
    return format_civil(seconds) + "Z"


def fmt_dur(seconds):
    if seconds % 3600 == 0:
        return f"{seconds // 3600}h"
    return f"{seconds}s"


def show(title):
    print(f"\n=== {title} ===")


def main():
    show("1. Local days across the fall-back transition (25h day)")
    window = (U(2024, 10, 26), U(2024, 10, 29))
    table = europe_like()
    segments = partition(window, table, DayGrid())
    for seg in segments:
        print(f"  {seg.label}  UTC [{fmt(seg.start)} .. {fmt(seg.end)})  true duration {fmt_dur(seg.duration)}")
    verify_segments_cover(segments, window)
    print("  coverage: union of segments == window, disjoint, exact")

    show("2. Custom week start (Sunday weeks vs Monday weeks)")
    window = (U(2024, 10, 26, 12), U(2024, 10, 30, 12))
    for week_start, name in ((6, "Sunday"), (0, "Monday")):
        segs = partition(window, OffsetTable.fixed(0), WeekGrid(week_start))
        print(f"  week_start={name}:")
        for seg in segs:
            print(f"    {seg.label}  UTC [{fmt(seg.start)} .. {fmt(seg.end)})  {fmt_dur(seg.duration)}")

    show("3. Months across the year boundary (no dropped period)")
    window = (U(2024, 12, 15), U(2025, 2, 10))
    segs = partition(window, OffsetTable.fixed(0), MonthGrid())
    for seg in segs:
        print(f"  {seg.label}  UTC [{fmt(seg.start)} .. {fmt(seg.end)})  {fmt_dur(seg.duration)}")
    verify_segments_cover(segs, window)

    show("4. One local day with two disjoint UTC preimages (30h offset jump)")
    wild = OffsetTable(0, [(U(2024, 1, 2, 12), -108000), (U(2024, 1, 4, 0), 0)])
    window = (U(2024, 1, 1), U(2024, 1, 5))
    segs = partition(window, wild, DayGrid())
    for seg in segs:
        print(f"  {seg.label}  UTC [{fmt(seg.start)} .. {fmt(seg.end)})  {fmt_dur(seg.duration)}")
    verify_segments_cover(segs, window)
    jan2 = [s for s in segs if s.label == "2024-01-02"]
    print(f"  label 2024-01-02 owns {len(jan2)} disjoint UTC pieces, "
          f"total {fmt_dur(sum(s.duration for s in jan2))}")

    show("5. Common refinement of overlapping calendars")
    window = (U(2024, 10, 26, 12), U(2024, 10, 28, 12))
    calendars = {
        "berlin": CalendarSpec(europe_like(), DayGrid()),
        "kolkata": CalendarSpec(OffsetTable.fixed(19800), DayGrid()),
        "iso-week": CalendarSpec(europe_like(), WeekGrid(0)),
    }
    ref = common_refinement(window, calendars)
    for seg in ref.segments:
        labels = ", ".join(f"{k}={v}" for k, v in seg.labels.items())
        print(f"  [{fmt(seg.start)} .. {fmt(seg.end)})  {fmt_dur(seg.duration)}  {labels}")
    verify_segments_cover(ref.segments, window)
    pre = ref.preimage_of("berlin", "2024-10-27")
    print(f"  preimages of berlin/2024-10-27: "
          + "; ".join(f"[{fmt(a)} .. {fmt(b)})" for a, b in pre)
          + f"  (total {fmt_dur(sum(b - a for a, b in pre))})")

    show("6. Local revision of the offset table")
    table = europe_like()
    print(f"  offset at 2025-06-01 before revision: {table.offset_at(U(2025, 6, 1))}s")
    table.revise(U(2025, 1, 1), U(2026, 1, 1), [])
    print(f"  offset at 2025-06-01 after cancelling 2025 DST: {table.offset_at(U(2025, 6, 1))}s")
    print(f"  offset at 2024-06-01 (outside the span, unchanged): {table.offset_at(U(2024, 6, 1))}s")

    show("7. Lazy pagination over a long window")
    window = (U(2024, 1, 1), U(2024, 2, 1))
    pager = SegmentPager(iter_segments(window, OffsetTable.fixed(0), DayGrid()), page_size=10)
    for page in pager:
        print(f"  page {pager.pages_served}: {len(page)} segments, "
              f"{page[0].label} .. {page[-1].label}")

    show("8. Reference verification: cut at every transition")
    stats = WorkStats()
    total, _ = verify_piecewise_conservation((U(2024, 1, 1), U(2025, 1, 1)), europe_like(), stats)
    print(f"  2024 window conserved exactly: {total}s over "
          f"{stats.pieces_checked} constant-offset pieces "
          f"({stats.transitions_scanned} transitions scanned)")

    show("9. Enormous window verified by jumping (no per-second scan)")
    start = U(2024, 1, 1)
    window = (start, start + 36_524_250 * 86400)  # 100 000 Gregorian years
    stats = WorkStats()
    total, _ = jump_scan(window, europe_like(), DayGrid(), stats)
    print(f"  window length: {total}s (~100 000 years)")
    print(f"  work recorded: {stats}")

    show("10. Wall clock vs instant vs duration (distinct types)")
    instant = Instant.from_civil(2024, 10, 27, 1, 30)
    wall = Wall.from_civil(2024, 10, 27, 1, 30)
    print(f"  instant: {instant!r}")
    print(f"  wall:    {wall!r}")
    print(f"  exact fraction endpoint: {fmt(Fraction(1, 3))}")
    try:
        instant - wall
    except TypeError as exc:
        print(f"  Instant - Wall is rejected: {exc}")


if __name__ == "__main__":
    main()
