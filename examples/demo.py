"""Runnable sample: partition UTC windows by local calendar boundaries.

Run from the repository root:

    python3.11 examples/demo.py
"""

import os
import sys
from datetime import date, datetime, timedelta, timezone
from fractions import Fraction

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from calpart import (  # noqa: E402
    DayCalendar,
    Interval,
    MonthCalendar,
    OffsetTable,
    Transition,
    WeekCalendar,
    WorkCounter,
    partition_window,
    reference_verify,
    verify_coverage,
)

EPOCH = datetime(1970, 1, 1, tzinfo=timezone.utc)


def utc(y, mo, d, h=0, mi=0, s=0):
    days = (date(y, mo, d) - date(1970, 1, 1)).days
    return Fraction(days * 86400 + h * 3600 + mi * 60 + s)


def fmt_utc(t: Fraction) -> str:
    whole = int(t)
    text = (EPOCH + timedelta(seconds=whole)).strftime("%Y-%m-%dT%H:%M:%SZ")
    frac = t - whole
    if frac:
        text += f" (+{frac} s)"
    return text


def fmt_dur(seconds: Fraction) -> str:
    whole = int(seconds)
    hh, rem = divmod(whole, 3600)
    mm, ss = divmod(rem, 60)
    text = f"{hh:02d}:{mm:02d}:{ss:02d}"
    frac = seconds - whole
    if frac:
        text += f" (+{frac} s)"
    return f"{text} = {seconds} s"


def show_segments(segments):
    for i, seg in enumerate(segments, 1):
        labels = "  ".join(f"{k}={v}" for k, v in seg.labels.items())
        print(f"  segment {i}: {labels}")
        for iv in seg.intervals:
            print(f"    UTC [{fmt_utc(iv.start)}, {fmt_utc(iv.end)})  len {fmt_dur(iv.length)}")
        print(f"    total duration: {fmt_dur(seg.duration.seconds)}")


def main():
    print("=" * 78)
    print("1) Fall-back window: day x week(start=Sunday) x month common refinement")
    print("=" * 78)
    table = OffsetTable(7200, [Transition(utc(2023, 10, 29, 1), 3600)])
    print("synthetic table: base +02:00, transition 2023-10-29T01:00:00Z -> +01:00")
    window = Interval(utc(2023, 10, 27), utc(2023, 11, 2))
    print(f"window: [{fmt_utc(window.start)}, {fmt_utc(window.end)})")
    calendars = [DayCalendar(), WeekCalendar(6), MonthCalendar()]
    segments = partition_window(table, window, calendars)
    show_segments(segments)
    report = verify_coverage(segments, window)
    print(f"coverage ok: {report.ok}; total {fmt_dur(report.total_duration.seconds)}")
    print(f"reference verify (cut at every transition point): "
          f"{reference_verify(table, window, segments)}")

    print()
    print("=" * 78)
    print("2) Offset jumps back 30h: one local date owns two disjoint UTC preimages")
    print("=" * 78)
    table2 = OffsetTable(0, [Transition(utc(2020, 6, 15, 0), -30 * 3600)])
    print("synthetic table: base +00:00, transition 2020-06-15T00:00:00Z -> -30:00")
    window2 = Interval(utc(2020, 6, 13), utc(2020, 6, 17))
    print(f"window: [{fmt_utc(window2.start)}, {fmt_utc(window2.end)})")
    segments2 = partition_window(table2, window2, [DayCalendar()])
    show_segments(segments2)
    report2 = verify_coverage(segments2, window2)
    print(f"coverage ok: {report2.ok}; total {fmt_dur(report2.total_duration.seconds)}")
    print(f"reference verify: {reference_verify(table2, window2, segments2)}")

    print()
    print("=" * 78)
    print("3) Extreme window: 5000 years by month, jump-based work accounting")
    print("=" * 78)
    days = (date(6970, 1, 1) - date(1970, 1, 1)).days
    window3 = Interval(0, days * 86400)
    work = WorkCounter()
    segments3 = partition_window(OffsetTable(0), window3, [MonthCalendar()], work)
    print(f"window length: {window3.length} s (~5000 years)")
    print(f"segments (months): {len(segments3)}")
    print(f"boundary jumps: {work.boundary_jumps}; transition visits: "
          f"{work.transition_visits}; wall lookups: {work.wall_lookups}")
    print(f"jump work / window seconds: {float(work.total() / window3.length):.2e} "
          f"(no per-second scanning)")
    report3 = verify_coverage(segments3, window3)
    print(f"coverage ok: {report3.ok}; total {report3.total_duration.seconds} s")


if __name__ == "__main__":
    main()
