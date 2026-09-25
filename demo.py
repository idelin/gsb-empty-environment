"""Runnable end-to-end demo: recurrence rules over a synthetic DST table.

Run with::

    python3 demo.py

It builds a +08:00/+09:00 daylight-saving fixture, defines daily, weekly and
monthly rules, merges exclusions and one-off additions, combines schedules
with set algebra, paginates a window, and reports jump counts to show large
queries never day-scan.
"""

from __future__ import annotations

import datetime as dt

from recur import (
    Ambiguous,
    DailyRule,
    Gap,
    Instant,
    MonthlyRule,
    STD,
    Schedule,
    WallTime,
    WeeklyRule,
    Window,
    difference,
    intersection,
    make_table,
    nth_event,
    query_page,
    union,
)


def instant_of(y, m, d, hh=0, mm=0):
    return Instant(WallTime.from_parts(y, m, d, hh, mm).epoch_us - STD)


def main():
    table = make_table()
    print(f"offset table version: {table.version}")

    daily = Schedule(
        DailyRule(WallTime.from_parts(2024, 1, 1, 8, 30), interval=1, name="daily"),
        table,
        exdates={dt.date(2024, 3, 12)},
        rdates=[WallTime.from_parts(2024, 3, 13, 20, 0)],
    )
    weekly = Schedule(
        WeeklyRule(
            WallTime.from_parts(2024, 1, 1, 8, 30),
            by_weekday=(0, 2, 4),  # Mon/Wed/Fri
            name="mwf",
        ),
        table,
    )
    monthly31 = Schedule(
        MonthlyRule(WallTime.from_parts(2024, 1, 31, 10), by_monthday=(31,), name="m31"),
        table,
    )

    window = Window(instant_of(2024, 3, 1), instant_of(2024, 4, 1))

    print("\n-- daily (with Mar 12 excluded, Mar 13 20:00 added) --")
    expansion = daily.expand(window)
    for event in expansion.events:
        if event.wall.day in (10, 12, 13):
            extra = f" -> mapped {event.mapped_wall.isoformat()}" if event.mapped_wall else ""
            print(f"  {event.instant.isoformat_utc()}  nominal={event.wall.isoformat()}  {event.kind}{extra}")
    print(f"  ... {len(expansion.events)} events, {expansion.jumps} period jumps")

    print("\n-- 31st-of-month across 2024 (short months skipped) --")
    year = Window(instant_of(2024, 1, 1), instant_of(2025, 1, 1))
    m31 = monthly31.expand(year)
    print("  months:", [f"{e.wall.month:02d}" for e in m31.events])
    print(f"  jumps: {m31.jumps} (13 month steps, not 366 days)")

    print("\n-- set algebra for March --")
    print("  union       :", len(union([daily, weekly]).expand(window).events), "events")
    print("  intersection:", [e.wall.day for e in intersection([daily, weekly]).expand(window).events])
    print("  difference  :", len(difference(daily, weekly).expand(window).events), "events")

    print("\n-- DST handling (spring gap / fall overlap) --")
    gap = table.resolve(WallTime.from_parts(2024, 3, 10, 2, 30), gap=Gap.FORWARD)
    print(f"  2024-03-10 02:30 is skipped -> {gap.adjusted_to.isoformat()} = {gap.instant.isoformat_utc()}")
    fold = table.resolve(
        WallTime.from_parts(2024, 11, 3, 1, 30), ambiguous=Ambiguous.EARLIEST
    )
    print(f"  2024-11-03 01:30 earliest   -> {fold.instant.isoformat_utc()} (offset +{fold.offset_us // 3600 // 1_000_000})")

    print("\n-- k-th occurrence (exclusions affect rank) --")
    print("  daily k=10 ->", nth_event(daily, 10).wall.date)

    print("\n-- pagination (limit 5, bound to schedule+table+window) --")
    cursor = None
    total = 0
    page_no = 0
    while True:
        page = query_page(daily, window, limit=5, cursor=cursor)
        page_no += 1
        days = [f"{e.wall.day:02d}@{e.wall.hour:02d}:{e.wall.minute:02d}" for e in page.events]
        print(f"  page {page_no}: {days} (jumps={page.jumps})")
        total += len(page.events)
        cursor = page.next_cursor
        if cursor is None:
            break
    print(f"  paged total: {total}")

    print("\n-- large span: 10 years, jumps vs days --")
    decade = Window(instant_of(2024, 1, 1), instant_of(2034, 1, 1))
    d10 = monthly31.expand(decade)
    days = (dt.date(2034, 1, 1) - dt.date(2024, 1, 1)).days
    print(f"  monthly-31: {len(d10.events)} events in {days} days using {d10.jumps} month jumps")


if __name__ == "__main__":
    main()
